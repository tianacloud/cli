package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
)

type createOptions struct {
	input          authclient.CreateInstanceRequest
	nonInteractive bool
	wait           bool
	json           bool
}

func executeSQLiteCreate(ctx context.Context, options createOptions, args []string, output, errorOutput io.Writer) int {
	return executeInstanceCreate(ctx, options, args, output, errorOutput, sqliteManagementScope)
}

// Save the request identity before POST and the receipt before output. No Token
// is created. Only --wait polls jobs. Unknown outcomes retain the same request ID.
func executeInstanceCreate(ctx context.Context, options createOptions, args []string, output, errorOutput io.Writer, scope databaseScope) (resultCode int) {
	var receipt any
	jsonWritten := false
	defer func() {
		if options.json && !jsonWritten && resultCode != 0 {
			_ = json.NewEncoder(output).Encode(apppublish.Result{Status: "failed", Data: receipt, Error: &apppublish.Error{Code: "CREATE_FAILED", Message: "Instance creation did not complete", NextAction: "Inspect stderr; repeat the same command to recover a pending request", ExitCode: resultCode}})
		}
	}()
	key := "db.create"
	if scope.engine == "git" {
		key = "git.create"
	}
	client, err := newAuthClient(ctx, errorOutput, options.nonInteractive)
	if err != nil {
		writeCommandError(errorOutput, err)
		return 1
	}
	store, err := newPendingStore()
	if err != nil {
		writeCommandError(errorOutput, err)
		return 1
	}
	release, err := store.Acquire(ctx)
	if err != nil {
		writeCommandError(errorOutput, err)
		return 1
	}
	defer release()
	pending, err := store.Load()
	existing := err == nil
	if err != nil && !errors.Is(err, authclient.ErrPendingNotFound) {
		writeCommandError(errorOutput, err)
		return 1
	}
	commandArgs := append([]string{"create"}, createIdentityArguments(args)...)
	if existing && (pending.Command != key || pending.Origin != client.Origin() || !sameStrings(pending.Args, commandArgs)) {
		reportUnfinishedOperation(errorOutput, store.Path, pending)
		return 1
	}
	if !existing {
		id, e := authclient.NewIdempotencyKey()
		if e != nil {
			writeCommandError(errorOutput, e)
			return 1
		}
		pending = authclient.PendingCommand{Command: key, Args: commandArgs, Origin: client.Origin(), IdempotencyKey: id, CreatedAt: time.Now().UTC()}
	}
	exitCode := 0
	operation := func(call context.Context, credential authclient.Credential) error {
		if pending.UserID != "" && pending.UserID != credential.User.ID {
			return errors.New("pending creation belongs to another account; original intent preserved")
		}
		pending.UserID = credential.User.ID
		if pending.InstanceID == "" {
			if !existing {
				engines, e := client.ListAppTypes(call)
				if e != nil {
					return e
				}
				if !engineAvailable(engines, options.input.Engine) {
					reportEngineUnavailable(errorOutput, options.input.Engine, engines)
					exitCode = 2
					return errCommandReported
				}
			}
			if pending.IdempotencyKey == "" {
				return errors.New("pending creation has no request identity")
			}
			if err := store.Save(pending); err != nil {
				return err
			}
			instance, e := client.CreateInstance(call, options.input, pending.IdempotencyKey)
			if e != nil {
				if definitiveCreateRejection(e) {
					if cleanup := store.Delete(); cleanup != nil {
						return cleanup
					}
				}
				return e
			}
			if instance.Engine != "" {
				if e := scope.check(instance); e != nil {
					return e
				}
			}
			pending.InstanceID = instance.ID
			pending.CreationOperationID = instance.CreationOperationID
			pending.CreationJobID = instance.CurrentJobID
			if err := store.Save(pending); err != nil {
				return err
			}
		} else if pending.CreationOperationID == "" && pending.CreationJobID == 0 {
			instance, e := client.GetInstance(call, pending.InstanceID)
			if e != nil {
				return e
			}
			if instance.ID != pending.InstanceID {
				return errors.New("creation metadata does not match pending instance")
			}
			if e := scope.check(instance); e != nil {
				return e
			}
			pending.CreationOperationID = instance.CreationOperationID
			pending.CreationJobID = instance.CurrentJobID
			if e := store.Save(pending); e != nil {
				return e
			}
		}
		receipt = struct {
			InstanceID  string `json:"instance_id"`
			JobID       uint64 `json:"job_id,omitempty"`
			OperationID string `json:"operation_id,omitempty"`
		}{pending.InstanceID, pending.CreationJobID, pending.CreationOperationID}
		receiptOutput := output
		if options.json {
			receiptOutput = errorOutput
		}
		if _, e := fmt.Fprintf(receiptOutput, "Creation accepted: instance=%s", safeDisplay(pending.InstanceID)); e != nil {
			return errors.New("cannot write creation receipt; repeat the same command to recover it")
		}
		if pending.CreationJobID != 0 {
			if _, e := fmt.Fprintf(receiptOutput, " job=%d", pending.CreationJobID); e != nil {
				return errors.New("cannot write creation receipt; repeat the same command to recover it")
			}
		}
		if pending.CreationOperationID != "" {
			if _, e := fmt.Fprintf(receiptOutput, " operation=%s", safeDisplay(pending.CreationOperationID)); e != nil {
				return errors.New("cannot write creation receipt; repeat the same command to recover it")
			}
		}
		if _, e := fmt.Fprintln(receiptOutput); e != nil {
			return errors.New("cannot write creation receipt; repeat the same command to recover it")
		}
		if options.wait {
			fmt.Fprintln(errorOutput, "Waiting for instance creation; Ctrl-C stops waiting without cancelling the server job.")
			if err := waitForInstanceCreation(call, client, pending, scope, time.Second); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(receiptOutput, "Creation succeeded: instance=%s\n", safeDisplay(pending.InstanceID)); err != nil {
				return errors.New("cannot write creation result; repeat the same command to recover it")
			}
		}
		if options.json {
			status := "accepted"
			if options.wait {
				status = "succeeded"
			}
			jsonWritten = true
			if err := json.NewEncoder(output).Encode(apppublish.Result{Status: status, Data: receipt}); err != nil {
				return errors.New("cannot write creation result; repeat the same command to recover it")
			}
		}
		return store.Delete()
	}
	err = client.RunAuthenticated(ctx, operation)
	if errors.Is(err, authclient.ErrAuthenticationRequired) {
		err = client.RunAuthenticated(ctx, operation)
	}
	if errors.Is(err, errCommandReported) {
		return exitCode
	}
	if err != nil {
		writeCommandError(errorOutput, err)
		if ctx.Err() != nil {
			return 130
		}
		return 1
	}
	return 0
}

func engineAvailable(engines []string, engine string) bool {
	for _, candidate := range engines {
		if candidate == engine {
			return true
		}
	}
	return false
}

func reportEngineUnavailable(errorOutput io.Writer, engine string, supported []string) {
	if len(supported) == 0 {
		fmt.Fprintf(errorOutput, "tiana: engine %q is not available; this environment has no published database engines\n", engine)
		return
	}
	fmt.Fprintf(errorOutput, "tiana: engine %q is not available; supported engines: %s\n", engine, safeDisplay(strings.Join(supported, ", ")))
}

// Preserve uncertain write outcomes for recovery with the original request ID.
func definitiveCreateRejection(err error) bool {
	var apiErr *authclient.APIError
	return errors.As(err, &apiErr) && apiErr.Code != "COMMIT_STATUS_UNKNOWN" && apiErr.Status < 500
}
