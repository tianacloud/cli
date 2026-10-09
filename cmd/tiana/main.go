package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/clientconfig"
	requestdiag "github.com/tianacloud/cli/internal/diagnostics"
	"github.com/tianacloud/cli/internal/releaseasset"
	"github.com/tianacloud/cli/internal/supervisor"
	"github.com/tianacloud/cli/internal/updatecheck"
	sdkauth "github.com/tianacloud/sdk-go/auth"
)

var (
	version = "1.0.0"
	// SQLite's pinned SDK still uses a build-selected physical Gateway port.
	gatewayPort = "443"
)

func main() {
	args := os.Args[1:]
	if len(args) == 1 && args[0] == updateWorkerArgument {
		(updatecheck.Checker{}).Check(context.Background(), map[string]string{updatecheck.CLIPackage: version}, updatecheck.Options{Fetch: true})
		return
	}
	if len(args) == 1 && args[0] == supervisor.BuiltinHelperArgument {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := supervisor.ServeBuiltinHelper(requestdiag.WithWriter(ctx, os.Stderr), os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		return
	}
	os.Exit(run(args))
}

func run(args []string) int {
	// Return stdout EPIPE to command error handling. Catching the signal keeps
	// exec children on their default SIGPIPE behavior instead of inheriting SIG_IGN.
	brokenPipe := make(chan os.Signal, 1)
	signal.Notify(brokenPipe, syscall.SIGPIPE)
	defer signal.Stop(brokenPipe)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runCLI(ctx, args, os.Stdin, os.Stdout, os.Stderr)
}

func runConnect(ctx context.Context, args []string, output, diagnostics io.Writer) int {
	for _, arg := range args[1:] {
		if arg == "--" {
			break
		}
		name := strings.SplitN(arg, "=", 2)[0]
		switch name {
		case "--token-env", "--token-file", "--token-stdin", "--non-interactive":
			fmt.Fprintln(diagnostics, "tiana: removed option; use TIANA_TOKEN/TIANA_TOKEN_FILE or account login for connect authentication")
			return 2
		}
	}
	parsed, err := supervisor.ParseCLI(args)
	if err != nil {
		fmt.Fprintln(diagnostics, "tiana:", safeDisplay(err.Error()))
		return 2
	}
	if parsed.Help {
		fmt.Fprintln(output, "Credentials: TIANA_TOKEN or TIANA_TOKEN_FILE; otherwise use the signed-in account access token. Run tiana login if no valid session exists.")
		for _, line := range strings.Split(supervisor.Usage(), "\n") {
			if strings.Contains(line, "--token-env") || strings.Contains(line, "--token-file") || strings.Contains(line, "--token-stdin") || strings.Contains(line, "--non-interactive") {
				continue
			}
			fmt.Fprintln(output, line)
		}
		return 0
	}
	if parsed.Connect == nil {
		fmt.Fprintln(diagnostics, "tiana: invalid connect command")
		return 2
	}

	if trust := clientconfig.FromContext(ctx); trust != nil && (!trust.IsDefault || parsed.Connect.Gateway.CAFile == "") {
		parsed.Connect.Gateway.CACertificates = trust.Certificates
		parsed.Connect.Gateway.CAFile = ""
	}
	launcher, _, err := resolveHelperLauncher()
	if err != nil {
		fmt.Fprintln(diagnostics, "tiana: trusted helper is unavailable")
		return 1
	}

	ctx = requestdiag.WithWriter(ctx, diagnostics)
	status, runErr := supervisor.NewSupervisor(launcher).Run(ctx, *parsed.Connect)
	if runErr != nil {
		fmt.Fprintln(diagnostics, "tiana:", safeDisplay(runErr.Error()))
		if status == 0 {
			return 1
		}
	}
	return status
}

func runLogin(ctx context.Context, output, errorOutput io.Writer) int {
	loginContext, cancel := context.WithCancel(ctx)
	defer cancel()
	progress := &loginProgressWriter{Writer: output, cancel: cancel}
	client, err := newAuthClient(loginContext, progress, false)
	if err != nil {
		fmt.Fprintln(errorOutput, "tiana:", safeDisplay(err.Error()))
		return 1
	}
	_, err = client.Login(loginContext)
	if progress.err != nil {
		writeCommandError(errorOutput, progress.err)
		return 1
	}
	if err != nil {
		writeCommandError(errorOutput, err)
		return 1
	}
	return 0
}

func runLogout(ctx context.Context, output, errorOutput io.Writer) int {
	client, err := newAuthClient(ctx, errorOutput, managementJSON(ctx))
	if err == nil {
		err = client.Logout(ctx)
	}
	if err != nil {
		if managementJSON(ctx) {
			return managementFailure(err, output, errorOutput, nil)
		}
		writeCommandError(errorOutput, err)
		return 1
	}
	if managementJSON(ctx) {
		return writeAppResult(apppublish.Success(map[string]bool{"logged_in": false}), true, output, errorOutput)
	}
	if _, err := fmt.Fprintln(output, "✓ Signed out"); err != nil {
		writeCommandError(errorOutput, err)
		return 1
	}
	return 0
}

func newAuthClient(ctx context.Context, output io.Writer, nonInteractive bool) (*authclient.Client, error) {
	origin, err := authclient.ResolveOrigin(ctx)
	if err != nil {
		return nil, err
	}
	store, err := authclient.NewCredentialStore(origin)
	if err != nil {
		return nil, err
	}
	config := authclient.Config{Origin: origin, Store: store, Output: output, NonInteractive: nonInteractive}
	config.OnRequestID = func(id string) { requestdiag.Write(ctx, "mgr", id) }
	if trust := clientconfig.FromContext(ctx); trust != nil {
		config.RootCAs = trust.Roots
	}
	return authclient.NewWithConfig(config)
}

func newPendingStore() (*authclient.FilePendingCommandStore, error) {
	path := strings.TrimSpace(os.Getenv("TIANA_PENDING_COMMAND_FILE"))
	if path == "" {
		var err error
		path, err = authclient.DefaultPendingCommandPath()
		if err != nil {
			return nil, err
		}
	}
	return authclient.NewFilePendingCommandStore(path), nil
}

func authUserLabel(user authclient.User) string {
	if user.Email != "" {
		return user.Email
	}
	if user.DisplayName != "" {
		return user.DisplayName
	}
	if user.Username != "" {
		return user.Username
	}
	return user.ID
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func writeCommandError(output io.Writer, err error) {
	defer func() {
		if id := sdkauth.RequestIDOf(err); id != "" {
			fmt.Fprintln(output, "Request ID:", safeDisplay(id))
		}
	}()
	if errors.Is(err, authclient.ErrAuthenticationRequired) {
		fmt.Fprintln(output, "tiana: authentication required; run tiana login")
		return
	}
	fmt.Fprintln(output, "tiana:", safeDisplay(err.Error()))
}

func resolveHelperLauncher() (supervisor.HelperLauncher, string, error) {
	assets := releaseasset.Current()
	if assets.Available() {
		launcher, err := supervisor.NewEmbeddedHelperLauncher(assets.Helper, assets.HelperSHA256)
		if err != nil {
			return nil, "", err
		}
		return launcher, "embedded", nil
	}
	return supervisor.BuiltinHelperLauncher{}, "builtin", nil
}

func resolveInstalledHelper() (supervisor.TrustedHelper, error) {
	executable, err := os.Executable()
	if err != nil {
		return supervisor.TrustedHelper{}, err
	}
	installRoot := filepath.Dir(filepath.Dir(executable))
	return supervisor.ResolveTrustedHelper(installRoot)
}
