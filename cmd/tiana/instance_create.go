package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

const defaultFirstTokenName = "default"

type createOptions struct {
	input          authclient.CreateInstanceRequest
	nonInteractive bool
}

type createState struct {
	instanceKey string
	instanceID  string
	endpointID  string
	step        string
	tokenKey    string
	requestID   string
	expiresAt   int64
	jobID       uint64
	tokenID     string
	instance    authclient.Instance
}

func executeSQLiteCreate(ctx context.Context, options createOptions, args []string, output, errorOutput io.Writer) int {
	return executeInstanceCreate(ctx, options, args, output, errorOutput, sqliteManagementScope)
}

func executeInstanceCreate(ctx context.Context, options createOptions, args []string, output, errorOutput io.Writer, scope databaseScope) int {
	commandKey := "db.create"
	if scope.engine == "git" {
		commandKey = "git.create"
	}
	client, err := newAuthClient(ctx, errorOutput, options.nonInteractive)
	if err != nil {
		fmt.Fprintln(errorOutput, "tiana:", safeDisplay(err.Error()))
		return 1
	}
	tokenStore, err := newInstanceTokenStore()
	if err != nil {
		fmt.Fprintln(errorOutput, "tiana:", safeDisplay(err.Error()))
		return 1
	}
	pendingStore, err := newPendingStore()
	if err != nil {
		fmt.Fprintln(errorOutput, "tiana:", safeDisplay(err.Error()))
		return 1
	}
	releasePending, err := pendingStore.Acquire(ctx)
	if err != nil {
		writeCommandError(errorOutput, err)
		return 1
	}
	defer releasePending()
	commandArgs := append([]string{"create"}, args...)
	origin := client.Origin()
	pending, pendingErr := pendingStore.Load()
	if pendingErr != nil && !errors.Is(pendingErr, authclient.ErrPendingNotFound) {
		fmt.Fprintln(errorOutput, "tiana: cannot read pending command:", safeDisplay(pendingErr.Error()))
		return 1
	}
	pendingMatches := pendingErr == nil && pending.Command == commandKey && pending.Origin == origin && sameStrings(pending.Args, commandArgs)
	if pendingErr == nil && !pendingMatches {
		reportUnfinishedOperation(errorOutput, pendingStore.Path, pending)
		return 1
	}
	state := createState{step: "instance", expiresAt: authclient.InstanceTokenNoExpiry}
	createdAt := time.Now().UTC()
	if pendingMatches {
		if pending.IdempotencyKey != "" {
			state.instanceKey = pending.IdempotencyKey
		}
		state.instanceID = pending.InstanceID
		state.endpointID = pending.EndpointID
		if pending.Step != "" {
			state.step = pending.Step
		}
		state.tokenKey = pending.TokenIdempotencyKey
		state.requestID = pending.TokenRequestID
		if pending.ExpiresAt != 0 {
			state.expiresAt = pending.ExpiresAt
		}
		state.jobID = pending.JobID
		state.tokenID = pending.TokenID
		if !pending.CreatedAt.IsZero() {
			createdAt = pending.CreatedAt
		}
	}
	if state.instanceKey == "" {
		if state.instanceKey, err = authclient.NewIdempotencyKey(); err != nil {
			fmt.Fprintln(errorOutput, "tiana:", safeDisplay(err.Error()))
			return 1
		}
	}
	savePending := func(userID string) error {
		return pendingStore.Save(authclient.PendingCommand{
			Command: commandKey, Args: commandArgs, IdempotencyKey: state.instanceKey,
			Origin: origin, UserID: userID, CreatedAt: createdAt,
			Step: state.step, InstanceID: state.instanceID, EndpointID: state.endpointID,
			TokenIdempotencyKey: state.tokenKey, TokenRequestID: state.requestID,
			TokenName: defaultFirstTokenName, ExpiresAt: state.expiresAt, JobID: state.jobID, TokenID: state.tokenID,
		})
	}
	deletePending := func() { _ = pendingStore.Delete() }
	exitCode := 0
	operation := func(operationContext context.Context, credential authclient.Credential) error {
		if pendingMatches && pending.UserID != "" && credential.User.ID != pending.UserID {
			// The pending operations belong to another account. Start this
			// account's create from the beginning with fresh identifiers.
			key, keyErr := authclient.NewIdempotencyKey()
			if keyErr != nil {
				return keyErr
			}
			state = createState{instanceKey: key, step: "instance", expiresAt: authclient.InstanceTokenNoExpiry}
			createdAt = time.Now().UTC()
			pendingMatches = false
		}
		if state.instanceID == "" && !pendingMatches {
			// A fresh create has no side effect yet, so reject an unavailable
			// engine before recording a resume intent or calling MGR.
			engines, listErr := client.ListAppTypes(operationContext)
			if listErr != nil {
				return listErr
			}
			if !engineAvailable(engines, options.input.Engine) {
				reportEngineUnavailable(errorOutput, options.input.Engine, engines)
				exitCode = 2
				return errCommandReported
			}
		}
		if state.instanceID == "" {
			if scope.engine == "git" {
				fmt.Fprintln(errorOutput, "Creating Git repository...")
			} else {
				fmt.Fprintln(errorOutput, "Creating database...")
			}
			// Persist the intent before the first side-effecting request so a
			// lost response resumes MGR with the same idempotency key.
			if saveErr := savePending(credential.User.ID); saveErr != nil {
				return saveErr
			}
			instance, createErr := client.CreateInstance(operationContext, options.input, state.instanceKey)
			if createErr != nil {
				if classifyWriteError(createErr) == tokenRejected {
					// MGR definitively rejected the request, so no instance
					// exists to resume and the pending intent is dead.
					deletePending()
				}
				return createErr
			}
			state.instance = instance
			state.instanceID = instance.ID
			state.step = "token"
			if scope.engine == "git" || instance.CreationOperationID != "" {
				state.step = "instance"
			}
			if state.tokenKey == "" {
				tokenKey, keyErr := authclient.NewTokenIdempotencyKey()
				if keyErr != nil {
					return keyErr
				}
				state.tokenKey = tokenKey
			}
			if state.requestID == "" {
				requestID, requestErr := authclient.NewRequestID()
				if requestErr != nil {
					return requestErr
				}
				state.requestID = requestID
			}
			if err := savePending(credential.User.ID); err != nil {
				return err
			}
		} else if state.instance.ID == "" {
			instance, getErr := client.GetInstance(operationContext, state.instanceID)
			if getErr != nil {
				if state.step != "instance" && (errors.Is(getErr, authclient.ErrInstanceNotFound) || errors.Is(getErr, authclient.ErrInstanceInvalidID)) {
					// Accepted creation may not be visible yet; preserve its ID.
					// Otherwise the recorded instance no longer exists, so the pending
					// intent can never be completed.
					deletePending()
				}
				return getErr
			}
			state.instance = instance
		}
		if state.step == "instance" {
			instance, waitErr := waitInstanceCreation(operationContext, client, state.instance, scope)
			if waitErr != nil {
				return waitErr
			}
			state.instance = instance
			state.step = "token"
			if err := savePending(credential.User.ID); err != nil {
				return err
			}
		}
		// Check even when resuming a legacy db.create intent. Do not mint a
		// Token for a different engine or discard a resumable intent.
		if err := scope.check(state.instance); err != nil {
			return err
		}
		if state.tokenKey == "" {
			tokenKey, keyErr := authclient.NewTokenIdempotencyKey()
			if keyErr != nil {
				return keyErr
			}
			state.tokenKey = tokenKey
		}
		if state.requestID == "" {
			requestID, requestErr := authclient.NewRequestID()
			if requestErr != nil {
				return requestErr
			}
			state.requestID = requestID
		}
		if err := bindTokenEndpoint(&state.endpointID, state.instance.EndpointID, state.jobID); err != nil {
			return err
		}
		// Persist the endpoint and identifiers before a possible Token write,
		// including recovery of legacy intents that did not record an endpoint.
		if err := savePending(credential.User.ID); err != nil {
			return err
		}
		fmt.Fprintln(errorOutput, "Creating first Token...")
		outcome := attemptInstanceToken(operationContext, client, tokenAttempt{
			InstanceID: state.instanceID, EndpointID: state.endpointID, Name: defaultFirstTokenName, ExpiresAt: state.expiresAt,
			RequestID: state.requestID, JobID: state.jobID, TokenID: state.tokenID,
		})
		switch outcome.Outcome {
		case tokenDelivered:
			exitCode = deliverCreatedDatabase(output, errorOutput, tokenStore, state.instance, outcome.Result)
			if exitCode == 0 {
				_ = pendingStore.Delete()
			}
			return errCommandReported
		case tokenUnknown:
			state.jobID = outcome.JobID
			state.tokenID = outcome.TokenID
			_ = savePending(credential.User.ID)
			fmt.Fprintln(errorOutput, "tiana: instance created, but the first Token result is not yet confirmed")
			fmt.Fprintf(errorOutput, "Instance ID: %s\n", safeDisplay(state.instanceID))
			if outcome.JobID != 0 {
				fmt.Fprintf(errorOutput, "Job ID: %d\n", outcome.JobID)
			}
			exitCode = 1
			return errCommandReported
		case tokenCommittedWithoutSecret:
			state.tokenKey, state.requestID, state.tokenID = "", "", ""
			state.jobID = 0
			_ = savePending(credential.User.ID)
			fmt.Fprintln(errorOutput, "tiana: instance created, but the first Token secret was not delivered")
			fmt.Fprintf(errorOutput, "Instance ID: %s\n", safeDisplay(state.instanceID))
			if outcome.Result.TokenID != "" {
				fmt.Fprintf(errorOutput, "Token ID: %s\n", safeDisplay(outcome.Result.TokenID))
			}
			fmt.Fprintln(errorOutput, "Revoke that Token in the existing console, confirm the revocation, then re-run to create a replacement.")
			exitCode = 1
			return errCommandReported
		case tokenAcceptedFailed:
			state.jobID = outcome.JobID
			state.tokenID = outcome.TokenID
			_ = savePending(credential.User.ID)
			fmt.Fprintln(errorOutput, "tiana: instance created, but the first Token delivery job failed; retry the job in the Console, then re-run this command to confirm it")
			fmt.Fprintf(errorOutput, "Instance ID: %s\n", safeDisplay(state.instanceID))
			if outcome.JobID != 0 {
				fmt.Fprintf(errorOutput, "Job ID: %d\n", outcome.JobID)
			}
			exitCode = 1
			return errCommandReported
		case tokenRejected, tokenFailed:
			state.tokenKey, state.requestID, state.tokenID = "", "", ""
			state.jobID = 0
			_ = savePending(credential.User.ID)
			fmt.Fprintf(errorOutput, "tiana: instance created, but the first Token failed: %s\n", safeDisplay(tokenFailureReason(outcome)))
			fmt.Fprintf(errorOutput, "Instance ID: %s\n", safeDisplay(state.instanceID))
			exitCode = 1
			return errCommandReported
		default:
			return outcome.Err
		}
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
		return 1
	}
	return 0
}

func deliverCreatedDatabase(output, errorOutput io.Writer, store authclient.InstanceTokenStore, instance authclient.Instance, token authclient.TokenResult) int {
	if instance.Engine == "git" {
		fmt.Fprintf(output, "Repository: %s\n", safeDisplay(instance.DisplayName))
	} else {
		fmt.Fprintf(output, "Database: %s\n", safeDisplay(instance.DisplayName))
	}
	fmt.Fprintf(output, "ID: %s\n", safeDisplay(instance.ID))
	fmt.Fprintf(output, "Engine: %s\n", safeDisplay(instance.Engine))
	fmt.Fprintf(output, "URL: %s\n", safeDisplay(connectionURLColumn(instance)))
	fmt.Fprintf(output, "Token ID: %s\n", safeDisplay(token.TokenID))
	fmt.Fprintf(output, "Token: %s\n", token.Token)
	fmt.Fprintf(output, "Token expires at: %s\n", safeDisplay(authclient.FormatExpiration(token.ExpiresAt)))
	location, err := saveInstanceTokenCredential(store, token, instance.ID)
	if err != nil {
		fmt.Fprintf(errorOutput, "tiana: could not save the Token locally: %v\n", safeDisplay(err.Error()))
		fmt.Fprintf(errorOutput, "Token ID: %s\n", safeDisplay(token.TokenID))
		return 1
	}
	fmt.Fprintf(output, "Token saved to: %s\n", safeDisplay(location))
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
