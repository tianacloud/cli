package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/localstate"
	"github.com/urfave/cli/v3"
)

func newStatusCommand(output, diagnostics io.Writer) *cli.Command {
	return &cli.Command{Name: "status", Usage: "Show login status and tenant quota", Flags: []cli.Flag{
		boolOption("json", "Write a structured JSON result"),
		boolOption("auth-only", "Check saved access-token expiry locally, without refresh or quota requests"),
	}, Description: "Shows the signed-in account and tenant usage/limits without starting browser login. Unavailable login or quota returns a nonzero exit status. With --auth-only, missing or expired credentials are a successful check with logged_in=false; server revocation is not checked.",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 0 {
				return argumentFailure(ctx, cmd, "this command does not accept arguments")
			}
			if cmd.Bool("auth-only") {
				return statusError(runAuthStatus(ctx, output, diagnostics))
			}
			return statusError(runStatus(ctx, output, diagnostics))
		}}
}

func runAuthStatus(ctx context.Context, output, diagnostics io.Writer) int {
	origin, err := authclient.ResolveOrigin(ctx)
	var credential authclient.Credential
	if err == nil {
		var store authclient.CredentialStore
		store, err = authclient.NewCredentialStore(origin)
		if err == nil {
			// Load directly: the SDK client's account path can lock and refresh.
			credential, err = store.Load()
			if err != nil && !errors.Is(err, authclient.ErrCredentialNotFound) {
				path, _ := authclient.DefaultCredentialPath()
				err = localstate.Wrap("read account credentials", path, err)
			}
		}
	}
	if err != nil && !errors.Is(err, authclient.ErrCredentialNotFound) {
		if managementJSON(ctx) {
			return managementFailure(err, output, diagnostics, nil)
		}
		writeCommandError(diagnostics, err)
		return 1
	}
	loggedIn := err == nil && time.Now().Before(credential.ExpiresAt)
	if managementJSON(ctx) {
		return writeAppResult(apppublish.Success(map[string]bool{"logged_in": loggedIn}), true, output, diagnostics)
	}
	state := "Not signed in"
	if loggedIn {
		state = "Signed in (local credentials)"
	}
	if _, err := fmt.Fprintln(output, state); err != nil {
		writeCommandError(diagnostics, err)
		return 1
	}
	return 0
}
