package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
	"github.com/urfave/cli/v3"
)

func newWebDeleteCommand(input io.Reader, output, diagnostics io.Writer) *cli.Command {
	return &cli.Command{Name: "delete", Usage: "Delete the Web instance and its current archive", ArgsUsage: "ID_OR_NAME", Description: "Keeps associated SQLite and Git resources. Requires terminal confirmation unless --force is set. Default success means deletion accepted; --wait waits for origin cleanup, unfinished uploads can also be deleted.", Flags: []cli.Flag{&cli.BoolFlag{Name: "force", Aliases: []string{"f"}, Usage: "Skip deletion confirmation", Local: true}, deleteWaitOption(), boolOption("json", "Write a structured JSON result")}, Action: func(ctx context.Context, cmd *cli.Command) error {
		if cmd.NArg() != 1 || strings.TrimSpace(cmd.Args().First()) == "" {
			return argumentFailure(ctx, cmd, "one Web ID or exact name is required")
		}
		if positionalWasTrimmed(cmd, cmd.Args().First()) {
			return argumentFailure(ctx, cmd, "use -- before the Web name to preserve surrounding whitespace")
		}
		return statusError(executeWebDelete(ctx, cmd.Args().First(), cmd.Bool("force"), cmd.Bool("wait"), cmd.Bool("json"), input, output, diagnostics))
	}}
}
func executeWebDelete(ctx context.Context, reference string, force, wait, jsonMode bool, input io.Reader, output, diagnostics io.Writer) int {
	fail := func(code, message, next string, exit int) int {
		return writeAppResult(apppublish.Failure(&apppublish.Error{Code: code, Message: message, NextAction: next, ExitCode: exit}), jsonMode, output, diagnostics)
	}
	if !force && !isTerminal(input) {
		return fail("CONFIRMATION_REQUIRED", "Deletion requires terminal confirmation", "Use --force (-f) for authorized non-interactive deletion", 2)
	}
	client, err := newAuthClient(ctx, diagnostics, !isTerminal(input))
	if err != nil {
		return fail("CREDENTIAL_UNAVAILABLE", "Cannot initialize account credentials", "Run tiana status", 1)
	}
	store, err := newPendingStore()
	if err != nil {
		return fail("PENDING_UNAVAILABLE", "Cannot read pending operations", "Inspect the pending command store", 1)
	}
	release, err := store.Acquire(ctx)
	if err != nil {
		return fail("PENDING_UNAVAILABLE", "Cannot lock pending operations", "Retry after the active operation finishes", 1)
	}
	defer release()
	pending, err := store.Load()
	existing := err == nil
	if err != nil && !errors.Is(err, authclient.ErrPendingNotFound) {
		return fail("PENDING_UNAVAILABLE", "Cannot read pending operations", "Inspect the pending command store", 1)
	}
	if existing && (pending.Command != "web.delete" || pending.Origin != client.Origin() || (reference != pending.InstanceID && !sameStrings(pending.Args, []string{"delete", reference}))) {
		reportUnfinishedOperation(diagnostics, store.Path, pending)
		if pending.Command == "web.create" {
			return fail("WEB_CREATE_IN_PROGRESS", "Web creation is unfinished; deletion is not allowed", "Recover the original web create command first", 1)
		}
		return fail("PENDING_OPERATION", "An unfinished operation must be completed first", "Recover the original operation before deletion", 1)
	}
	r, e := (apppublish.Runner{Client: client}).Bind(ctx)
	if e != nil {
		return writeAppResult(apppublish.Failure(e), jsonMode, output, diagnostics)
	}
	var target apppublish.WebProject
	if existing {
		if pending.UserID != r.PrincipalID || pending.TenantID != r.TenantID {
			return fail("PENDING_ACCOUNT_CHANGED", "Pending deletion belongs to another account", "Restore the original account to recover the same deletion", 1)
		}
		target = apppublish.WebProject{ID: pending.InstanceID, Name: reference}
	} else {
		target, e = r.Resolve(ctx, reference)
		if e != nil {
			return writeAppResult(apppublish.Failure(e), jsonMode, output, diagnostics)
		}
		if reference != target.ID {
			target, e = r.Resolve(ctx, target.ID)
			if e != nil {
				return writeAppResult(apppublish.Failure(e), jsonMode, output, diagnostics)
			}
		}
	}
	if !force {
		if _, err = fmt.Fprintf(diagnostics, "Delete application %q (%s), including its instance and current archive? SQLite and Git are kept. [y/N] ", safeDisplay(target.Name), safeDisplay(target.ID)); err != nil {
			return 1
		}
		accepted, err := readDeleteConfirmation(ctx, input.(*os.File))
		if err != nil {
			if ctx.Err() != nil {
				return 130
			}
			return fail("CONFIRMATION_FAILED", "Cannot read deletion confirmation", "Retry the command", 1)
		}
		if !accepted {
			if jsonMode {
				return writeAppResult(apppublish.Result{Status: "cancelled"}, true, output, diagnostics)
			}
			fmt.Fprintln(diagnostics, "Deletion cancelled.")
			return 0
		}
	}
	if !existing {
		key, err := authclient.NewIdempotencyKey()
		if err != nil {
			return fail("PENDING_UNAVAILABLE", "Cannot generate deletion intent", "Retry the same command", 1)
		}
		pending = authclient.PendingCommand{Command: "web.delete", Args: []string{"delete", reference}, InstanceID: target.ID, Origin: client.Origin(), UserID: r.PrincipalID, TenantID: r.TenantID, IdempotencyKey: key, CreatedAt: time.Now().UTC()}
	}
	if err = store.Save(pending); err != nil {
		return fail("PENDING_UNAVAILABLE", "Cannot persist deletion intent; no request sent", "Inspect the pending command store", 1)
	}
	d, e := r.Deletion(ctx, target.ID, &apppublish.WebProjectDeleteRequest{})
	if e != nil && e.HTTPStatus == 409 && e.Code == "WEB_DELETE_SELECTION_CONFLICT" {
		d, e = r.Deletion(ctx, target.ID, nil)
	}
	if e != nil {
		result := apppublish.Failure(e)
		result.Data = map[string]string{"id": target.ID}
		code := writeAppResult(result, jsonMode, output, diagnostics)
		if ctx.Err() != nil {
			return 130
		}
		return code
	}
	if wait {
		if !jsonMode {
			fmt.Fprintf(diagnostics, "Deletion accepted: web_id=%s; waiting for origin cleanup. Ctrl-C stops waiting only.\n", safeDisplay(d.ID))
		}
		for d.State != "deleted" {
			timer := time.NewTimer(2 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return writeAppResult(apppublish.Result{Status: "pending", Data: d, Error: &apppublish.Error{RequestID: d.RequestID, Code: "WAIT_INTERRUPTED", Message: "Stopped waiting; server deletion continues", NextAction: "Retry web delete with the same ID and --wait", ExitCode: 130}}, jsonMode, output, diagnostics)
			case <-timer.C:
			}
			next, e := r.Deletion(ctx, target.ID, nil)
			if e != nil {
				return writeAppResult(apppublish.Result{Status: "pending", Data: d, Error: e}, jsonMode, output, diagnostics)
			}
			d = next
		}
	}
	if jsonMode {
		code := writeAppResult(apppublish.Success(d), true, output, diagnostics)
		if code != 0 {
			return code
		}
		if err = store.Delete(); err != nil {
			writeCommandError(diagnostics, err)
			return 1
		}
		return 0
	}
	action := "accepted"
	if d.State == "deleted" {
		action = "completed"
	}
	if _, err = fmt.Fprintf(output, "Deletion %s: web_id=%s state=%s\n", action, safeDisplay(d.ID), d.State); err != nil {
		return 1
	}
	if err = store.Delete(); err != nil {
		writeCommandError(diagnostics, err)
		return 1
	}
	return 0
}
