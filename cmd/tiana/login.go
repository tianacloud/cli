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
		if pending {
			status, code, next, exitCode = "pending", "AUTH_PENDING", "Open verification_uri, approve access, then run tiana login --resume --json", 3
		} else if err != nil {
			status, code, next, exitCode = "failed", "AUTH_REQUIRED", "Run tiana login --start --no-open --json for a sign-in link", 5
		}
		data := map[string]any{"logged_in": exitCode == 0}
		if pending {
			data["verification_uri"] = transaction.VerificationURIComplete
			data["expires_at"] = transaction.CreatedAt.Add(time.Duration(transaction.ExpiresIn) * time.Second)
			data["retry_after"] = int(transaction.PollInterval / time.Second)
		}
		if cmd.Bool("json") {
			result := map[string]any{"status": status, "data": data, "error": nil}
			if exitCode != 0 {
				result["error"] = map[string]string{"code": code, "next_action": next}
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
