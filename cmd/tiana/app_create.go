package main

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
)

// Serialize creation with the same durable intent store used by SQLite/Git.
// Save before POST, retain across transport/output failures, clear after output.
func executeAppCreate(ctx context.Context, client *authclient.Client, o apppublish.Options, jsonMode bool, output, diagnostics io.Writer) int {
	fail := func(message string) int {
		return writeAppResult(apppublish.Failure(&apppublish.Error{Code: "APP_CREATE_PENDING", Message: message, NextAction: "Resolve the pending creation and repeat the same command", ExitCode: 1}), jsonMode, output, diagnostics)
	}
	store, err := newPendingStore()
	if err != nil {
		return fail("Cannot open the pending command store")
	}
	release, err := store.Acquire(ctx)
	if err != nil {
		return fail("Cannot lock the pending command store")
	}
	defer release()
	pending, err := store.Load()
	exists := err == nil
	if err != nil && !errors.Is(err, authclient.ErrPendingNotFound) {
		return fail("Cannot read the pending command store")
	}
	args := []string{"create", o.Name}
	if o.Description != "" {
		args = append(args, "--description", o.Description)
	}
	if exists && ((pending.Command != "apps.create" && pending.Command != "web.create") || pending.Origin != client.Origin() || !sameStrings(pending.Args, args)) {
		reportUnfinishedOperation(diagnostics, store.Path, pending)
		return fail("An unfinished product operation must be completed first")
	}
	credential, err := client.EnsureCredential(ctx)
	if err != nil {
		return writeAppResult(apppublish.Failure(&apppublish.Error{Code: "AUTH_REQUIRED", Message: "Sign in before creating an application", NextAction: "Run tiana login", ExitCode: 5}), jsonMode, output, diagnostics)
	}
	if exists && (pending.UserID != credential.User.ID || pending.TenantID != credential.User.TenantID) {
		return fail("Pending creation belongs to another account; original intent preserved")
	}
	if !exists {
		key, err := authclient.NewIdempotencyKey()
		if err != nil {
			return fail("Cannot generate a request identity")
		}
		pending = authclient.PendingCommand{Command: "apps.create", Args: args, Origin: client.Origin(), IdempotencyKey: key, UserID: credential.User.ID, TenantID: credential.User.TenantID, CreatedAt: time.Now().UTC()}
	}
	if err = store.Save(pending); err != nil {
		return fail("Cannot persist the creation intent; no request sent")
	}
	o.RequestID = pending.IdempotencyKey
	result := (apppublish.Runner{Client: client, PrincipalID: pending.UserID, TenantID: pending.TenantID}).Run(ctx, o)
	code := writeAppResult(result, jsonMode, output, diagnostics)
	definitive := result.Error != nil && (result.Error.HTTPStatus == 400 || result.Error.HTTPStatus == 404 || result.Error.HTTPStatus == 405 || result.Error.HTTPStatus == 422)
	if (result.Error == nil && code == 0) || definitive {
		if err = store.Delete(); err != nil {
			// The receipt may already have been emitted. Avoid a second JSON document.
			writeCommandError(diagnostics, errors.New("cannot clear App creation intent; repeat the same command to recover"))
			return 1
		}
	}
	if ctx.Err() != nil {
		return 130
	}
	return code
}
