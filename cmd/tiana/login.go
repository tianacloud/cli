package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/localstate"
	"github.com/urfave/cli/v3"
)

type loginProgressWriter struct {
	io.Writer
	cancel context.CancelFunc
	err    error
}

func (w *loginProgressWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.Writer.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.err = err
		w.cancel()
	}
	return n, err
}

func newLoginCommand(output, diagnostics io.Writer) *cli.Command {
	return &cli.Command{Name: "login", Usage: "Sign in through a browser", Flags: []cli.Flag{
		&cli.BoolFlag{Name: "start", Usage: "Return a reusable browser sign-in link without waiting"},
		&cli.BoolFlag{Name: "resume", Usage: "Check browser approval once and save the account session"},
		&cli.BoolFlag{Name: "json", Usage: "Emit structured login progress"},
		&cli.BoolFlag{Name: "no-open", Usage: "Print the login link without opening a browser"},
	}, Action: func(ctx context.Context, cmd *cli.Command) error {
		if cmd.NArg() != 0 || (cmd.Bool("start") && cmd.Bool("resume")) || (cmd.Bool("json") && !cmd.Bool("start") && !cmd.Bool("resume")) {
			return argumentFailure(ctx, cmd, "choose --start or --resume for JSON login")
		}
		if !cmd.Bool("start") && !cmd.Bool("resume") {
			return statusError(runLogin(ctx, output, diagnostics))
		}
		client, err := newAuthClient(ctx, io.Discard, true)
		var transaction authclient.AuthTransaction
		if err == nil {
			if cmd.Bool("start") {
				transaction, err = client.StartLogin(ctx)
			} else {
				transaction, err = client.ResumeLogin(ctx)
			}
		}
		pending := errors.Is(err, authclient.ErrAuthorizationPending) || (err == nil && cmd.Bool("start"))
		status, code, next, exitCode := "succeeded", "", "", 0
		message := ""
		if pending {
			status, code, next, exitCode = "pending", "AUTH_PENDING", "Open verification_uri, approve access, then run tiana login --resume --json", 3
		} else if err != nil {
			status = "failed"
			code, message, next, exitCode = loginFailure(err)
			if exitCode == 4 {
				status = "unknown"
			}
		}
		data := map[string]any{}
		if pending {
			data["pending_auth"] = true
		}
		if errors.Is(err, authclient.ErrLoginNotStarted) {
			data["pending_auth"] = false
		}
		if exitCode == 0 {
			data["logged_in"] = true
			data["pending_auth"] = false
		}
		if pending {
			data["verification_uri"] = transaction.VerificationURIComplete
			data["expires_at"] = transaction.CreatedAt.Add(time.Duration(transaction.ExpiresIn) * time.Second)
			data["retry_after"] = int(transaction.PollInterval / time.Second)
		}
		if cmd.Bool("json") {
			result := map[string]any{"status": status, "data": data, "error": nil}
			if exitCode != 0 {
				failure := map[string]string{"code": code, "message": message}
				if next != "" {
					failure["next_action"] = next
				}
				result["error"] = failure
			}
			if json.NewEncoder(output).Encode(result) != nil {
				return statusError(1)
			}
		} else if pending {
			if _, err := fmt.Fprintln(output, transaction.VerificationURIComplete); err != nil {
				writeCommandError(diagnostics, err)
				return statusError(1)
			}
			if _, err := fmt.Fprintln(output, next); err != nil {
				writeCommandError(diagnostics, err)
				return statusError(1)
			}
		} else if err != nil {
			writeCommandError(diagnostics, err)
		} else {
			if _, err := fmt.Fprintln(output, "✓ Signed in"); err != nil {
				writeCommandError(diagnostics, err)
				return statusError(1)
			}
		}
		if pending && cmd.Bool("start") && !cmd.Bool("no-open") && !cmd.Bool("json") {
			openLoginBrowser(ctx, transaction.VerificationURIComplete)
		}
		return statusError(exitCode)
	}}
}

func openLoginBrowser(ctx context.Context, uri string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := loginBrowserCommand(ctx, runtime.GOOS, uri)
	if command != nil {
		_ = command.Run()
	}
}

func loginBrowserCommand(ctx context.Context, platform, uri string) *exec.Cmd {
	switch platform {
	case "darwin":
		return exec.CommandContext(ctx, "open", uri)
	case "linux":
		return exec.CommandContext(ctx, "xdg-open", uri)
	case "windows":
		return exec.CommandContext(ctx, "rundll32.exe", "url.dll,FileProtocolHandler", uri)
	default:
		return nil
	}
}

// A command outcome is not the current account's authentication state.
func loginFailure(err error) (code, message, next string, exit int) {
	var local *localstate.Error
	if errors.As(err, &local) {
		next = "Set XDG_CONFIG_HOME to a writable configuration directory, or repair the local path permissions"
		if local.Code == "LOCAL_STATE_BUSY" {
			next = "Wait for the other CLI command to finish, then retry"
		}
		if local.Code == "LEGACY_PENDING_COMMAND" {
			next = "Set TIANA_PENDING_COMMAND_FILE to the previous path and recover the original command"
		}
		return local.Code, safeDisplay(local.Error()), next, 1
	}
	switch {
	case errors.Is(err, authclient.ErrCredentialSaveFailed):
		return "LOCAL_STATE_UNAVAILABLE", "Cannot safely persist local authorization state", "Set XDG_CONFIG_HOME to a writable directory and check the existing state file permissions", 1
	case errors.Is(err, authclient.ErrLoginNotStarted):
		return "NO_PENDING_AUTH", "No pending browser authorization to resume; current login state was not checked", "Run tiana status to check the current account", 1
	case errors.Is(err, authclient.ErrTransactionExpired):
		return "AUTH_EXPIRED", "Browser authorization expired", "Run tiana login --start --no-open --json for a new sign-in link", 5
	case errors.Is(err, authclient.ErrTransactionDenied):
		return "AUTH_DENIED", "Browser authorization was declined", "", 5
	case errors.Is(err, authclient.ErrTransactionCompleted):
		return "AUTH_OUTCOME_UNKNOWN", "Authorization was already exchanged; current account state must be checked", "Run tiana status before starting another authorization", 4
	case errors.Is(err, authclient.ErrAuthenticationRequired):
		return "AUTH_REQUIRED", "Account authentication is required", "Run tiana login --start --no-open --json for a sign-in link", 5
	case errors.Is(err, context.Canceled):
		return "CANCELLED", "Login was cancelled", "", 130
	default:
		return "AUTH_UNAVAILABLE", "Cannot complete the authorization request", "Check network availability and current account status before continuing", 1
	}
}
