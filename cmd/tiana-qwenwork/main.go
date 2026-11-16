// tiana-qwenwork is the native entrypoint packaged by the QwenWork connector.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/clientconfig"
	"github.com/tianacloud/cli/internal/netconfig"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	executable, err := os.Executable()
	if err != nil {
		os.Exit(failure(os.Stderr, "E-9999", "Cannot locate connector executable", 1))
	}
	os.Exit(run(ctx, executable, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func child(ctx context.Context, executable string, args []string, input io.Reader, output, diagnostics io.Writer) int {
	name := "tiana"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	command := exec.CommandContext(ctx, filepath.Join(filepath.Dir(executable), "libexec", name), args...)
	command.Stdin = input
	command.Stdout = output
	command.Stderr = diagnostics
	command.Cancel = func() error {
		if runtime.GOOS == "windows" {
			return command.Process.Kill()
		}
		return command.Process.Signal(os.Interrupt)
	}
	command.WaitDelay = 5 * time.Second
	err := command.Run()
	if ctx.Err() != nil {
		return 130
	}
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() >= 0 {
		return exit.ExitCode()
	}
	return failure(diagnostics, "E-9999", "Cannot run bundled Tiana CLI", 1)
}

func failure(w io.Writer, code, message string, exit int) int {
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
	return exit
}
func result(w, diagnostics io.Writer, value any) int {
	if json.NewEncoder(w).Encode(value) != nil {
		return failure(diagnostics, "E-9999", "Cannot write result", 1)
	}
	return 0
}

func accountClient(ctx context.Context) (*authclient.Client, error) {
	trust, err := clientconfig.Load(os.Getenv("TIANA_CA_FILE"))
	if err != nil {
		return nil, err
	}
	ctx = clientconfig.WithTrust(ctx, trust)
	transport, err := netconfig.NewTransport(ctx)
	if err != nil {
		return nil, err
	}
	origin, err := authclient.ResolveOrigin(ctx)
	if err != nil {
		return nil, err
	}
	store, err := authclient.NewCredentialStore(origin)
	if err != nil {
		return nil, err
	}
	return authclient.NewWithConfig(authclient.Config{Origin: origin, Store: store, RootCAs: trust.Roots, HTTPClient: &http.Client{Transport: transport}, NonInteractive: true})
}
func accountFailure(w io.Writer, err error) int {
	if errors.Is(err, authclient.ErrAuthenticationRequired) {
		return failure(w, "E-0001", "Sign in to Tiana", 1)
	}
	var api *authclient.APIError
	if errors.As(err, &api) {
		if api.Status == http.StatusForbidden {
			return failure(w, "E-0005", "Access denied", 1)
		}
		return failure(w, "E-0004", "Tiana service request failed", 1)
	}
	var network net.Error
	if errors.As(err, &network) {
		return failure(w, "E-0004", "Cannot reach Tiana", 1)
	}
	if errors.Is(err, os.ErrPermission) {
		return failure(w, "E-0005", "Cannot access account storage", 1)
	}
	return failure(w, "E-9999", "Cannot read or update Tiana account state", 1)
}

func run(ctx context.Context, executable string, args []string, input io.Reader, output, diagnostics io.Writer) int {
	if slices.Equal(args, []string{"--version"}) {
		data, err := os.ReadFile(filepath.Join(filepath.Dir(executable), "..", "..", ".qwenwork-plugin", "plugin.json"))
		var manifest struct {
			Version string `json:"version"`
		}
		if err != nil || json.Unmarshal(data, &manifest) != nil || manifest.Version == "" {
			return failure(diagnostics, "E-9999", "Cannot read connector version", 1)
		}
		if _, err = fmt.Fprintln(output, manifest.Version); err != nil {
			return failure(diagnostics, "E-9999", "Cannot write version", 1)
		}
		return 0
	}
	if len(args) == 0 || args[0] != "auth" && args[0] != "identity" {
		return child(ctx, executable, args, input, output, diagnostics)
	}
	switch {
	case slices.Equal(args, []string{"auth", "init"}):
		var out bytes.Buffer
		code := child(ctx, executable, []string{"login", "--json"}, input, &out, io.Discard)
		if code == 0 {
			return result(output, diagnostics, map[string]string{"status": "authorized"})
		}
		var response struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(out.Bytes(), &response)
		qcode := "E-9999"
		switch response.Error.Code {
		case "AUTH_REQUIRED":
			qcode = "E-0001"
		case "AUTH_EXPIRED":
			qcode = "E-0002"
		case "INVALID_ARGUMENT":
			qcode = "E-0003"
		case "AUTH_DENIED":
			qcode = "E-0005"
		case "NETWORK_ERROR", "UPSTREAM_ERROR":
			qcode = "E-0004"
		}
		if code == 130 {
			return failure(diagnostics, qcode, "Sign-in cancelled", code)
		}
		return failure(diagnostics, qcode, "Tiana sign-in did not complete", code)
	case slices.Equal(args, []string{"auth", "show", "--status"}), slices.Equal(args, []string{"identity", "whoami"}), slices.Equal(args, []string{"auth", "logout"}):
		client, err := accountClient(ctx)
		if err != nil {
			return accountFailure(diagnostics, err)
		}
		if args[1] == "logout" {
			if err = client.Logout(ctx); err != nil {
				return accountFailure(diagnostics, err)
			}
			return result(output, diagnostics, map[string]string{"status": "unauthorized"})
		}
		user, err := client.Whoami(ctx)
		if args[0] == "auth" && errors.Is(err, authclient.ErrAuthenticationRequired) {
			return result(output, diagnostics, map[string]string{"status": "unauthorized"})
		}
		if err != nil {
			return accountFailure(diagnostics, err)
		}
		if args[0] == "auth" {
			return result(output, diagnostics, map[string]string{"status": "authorized"})
		}
		return result(output, diagnostics, user)
	default:
		return failure(diagnostics, "E-0003", "Expected auth init, auth show --status, auth logout or identity whoami", 2)
	}
}
