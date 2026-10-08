package main

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/clientconfig"
	"github.com/tianacloud/cli/internal/sqlitecli"
	tiana "github.com/tianacloud/sdk-go"
	tianasqlite "github.com/tianacloud/sdk-go-sqlite"
)

type sqliteOptions struct {
	command, reference, branch, sql, file, format, output string
	endpoint, port                                        string
	timeout                                               time.Duration
	nonInteractive                                        bool
}

type sqliteResolution struct {
	instance authclient.Instance
	client   *authclient.Client
}

type sqliteResolver func(context.Context, string, bool) (sqliteResolution, error)

func resolveSQLite(ctx context.Context, reference, branch string, nonInteractive bool, diagnostics io.Writer) (sqliteResolution, error) {
	client, err := newAuthClient(ctx, diagnostics, nonInteractive)
	if err != nil {
		return sqliteResolution{}, err
	}
	instance, _, err := resolveSQLiteBranchWithLogin(ctx, client, reference, branch)
	return sqliteResolution{instance: instance, client: client}, err
}
func sqliteEndpoint(instance authclient.Instance) (string, string, error) {
	if instance.Engine != "sqlite" {
		return "", "", fmt.Errorf("instance engine must be sqlite")
	}
	if instance.Connection == nil {
		return "", "", fmt.Errorf("instance has no canonical connection endpoint yet")
	}
	host, err := tiana.ParseEndpoint(instance.Connection.Hostname)
	if err != nil {
		return "", "", fmt.Errorf("instance has an invalid connection endpoint")
	}
	if instance.EndpointID != "" {
		// ParseEndpoint validates the full deployment hostname. EndpointID is
		// a bare identity from MGR, not another connection hostname.
		hostID, _, _ := strings.Cut(host, ".")
		if !strings.EqualFold(hostID, instance.EndpointID) {
			return "", "", fmt.Errorf("instance endpoint metadata is inconsistent")
		}
	}
	port := "443"
	if raw := strings.TrimSpace(instance.Connection.URL); raw != "" {
		parsed, parseErr := url.Parse(raw)
		if parseErr != nil || parsed.Scheme != "https" || parsed.Hostname() != instance.Connection.Hostname || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", "", fmt.Errorf("instance has an invalid connection URL")
		}
		if parsed.Port() != "" {
			port = parsed.Port()
		}
	}
	return host, port, nil
}

// A direct locator selects the Gateway authority, never an arbitrary HTTP route.
// Keep diagnostics constant: malformed URLs can contain accidentally pasted secrets.
func parseSQLiteDirectEndpoint(raw string) (string, string, error) {
	invalid := func() (string, string, error) {
		return "", "", errors.New("invalid --endpoint; use an HTTPS Endpoint URL or hostname[:port], without credentials, paths, queries or fragments")
	}
	if raw == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "?#") {
		return invalid()
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || strings.ContainsAny(u.Host, "[]") || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return invalid()
	}
	host, err := tiana.ParseEndpoint(u.Hostname())
	if err != nil {
		return invalid()
	}
	port := "443"
	if strings.Contains(u.Host, ":") {
		n, err := strconv.ParseUint(u.Port(), 10, 16)
		if err != nil || n == 0 {
			return invalid()
		}
		port = strconv.FormatUint(n, 10)
	}
	return host, port, nil
}

func runSQLite(ctx context.Context, args []string, input io.Reader, output, diagnostics io.Writer) int {
	return runCLI(ctx, append([]string{"sqlite"}, args...), input, output, diagnostics)
}

// The injected resolver/SDK config keep tests on the command's real preflight and
// output paths without account credentials or a deployed database.
func runSQLiteWith(ctx context.Context, args []string, input io.Reader, output, diagnostics io.Writer, resolve sqliteResolver, config *tianasqlite.Config) int {
	return runCLIWithSQL(ctx, append([]string{"sqlite"}, args...), input, output, diagnostics,
		func(ctx context.Context, o sqliteOptions) int {
			return executeSQLite(ctx, o, input, output, diagnostics, resolve, config)
		})
}

func executeSQLite(ctx context.Context, o sqliteOptions, input io.Reader, output, diagnostics io.Writer, resolve sqliteResolver, config *tianasqlite.Config) int {
	var err error
	emit := func(e *sqlitecli.Error) int {
		if e == nil {
			return 0
		}
		fmt.Fprintln(diagnostics, e.Error())
		return e.ExitCode
	}
	var statements []sqlitecli.Statement
	if o.command != "shell" {
		sql := o.sql
		if o.command == "run" {
			var e *sqlitecli.Error
			sql, e = sqlitecli.ReadScript(o.file)
			if e != nil {
				return emit(e)
			}
		}
		var e *sqlitecli.Error
		statements, e = sqlitecli.Split(sql)
		if e != nil {
			return emit(e)
		}
		if len(statements) == 0 || (o.command == "exec" && (len(statements) != 1 || statements[0].Transaction)) {
			fmt.Fprintln(diagnostics, "tiana: exec requires one non-transaction statement; scripts must not be empty")
			return 2
		}
	}
	var roots *x509.CertPool
	if trust := clientconfig.FromContext(ctx); trust != nil {
		roots = trust.Roots
	}
	var outputFile *os.File
	if o.output != "" {
		outputFile, err = sqlitecli.CreateOutput(o.output)
		if err != nil {
			fmt.Fprintln(diagnostics, "tiana: cannot exclusively create output file")
			return 6
		}
		defer outputFile.Close()
		output = outputFile
	}
	var e *sqlitecli.Error
	rawToken, credentialErr := authclient.ConnectionCredential(ctx)
	if credentialErr != nil {
		if ctx.Err() != nil {
			return 130
		}
		fmt.Fprintln(diagnostics, "tiana:", safeDisplay(credentialErr.Error()))
		return 2
	}
	token, tokenErr := tiana.NewToken(string(rawToken))
	clear(rawToken)
	if tokenErr != nil {
		fmt.Fprintln(diagnostics, "tiana: invalid connection credential")
		return 2
	}
	endpoint, port := o.endpoint, o.port
	var instance authclient.Instance
	var resolution sqliteResolution
	if endpoint == "" {
		resolution, err = resolve(ctx, o.reference, true)
		if err != nil {
			if ctx.Err() != nil {
				return emit(&sqlitecli.Error{Code: "INTERRUPTED", Message: "interrupted before SQL was sent", Outcome: "not_sent", ExitCode: 130})
			}
			reportResolveError(diagnostics, o.reference, err)
			return 1
		}
		instance = resolution.instance
		endpoint, port, err = sqliteEndpoint(instance)
		if err != nil {
			fmt.Fprintln(diagnostics, "tiana:", safeDisplay(err.Error()))
			return 2
		}
	}

	if config == nil {
		resolved := sqlitecli.SDKConfig(endpoint, port, token, roots, o.timeout)
		config = &resolved
	}
	client := sqlitecli.NewClient(*config, o.timeout)
	defer client.Close()
	results := sqlitecli.NewOutput(output, o.format)
	switch o.command {
	case "exec":
		r, execErr := client.Execute(ctx, statements[0].SQL, true)
		e = execErr
		if e == nil {
			e = results.Result(1, r)
		}
	case "run":
		e = sqlitecli.Script(ctx, client, results, statements)
	case "shell":
		interactive := !o.nonInteractive && sqliteTerminal(input) && sqliteTerminal(output)
		e = sqlitecli.Shell(ctx, client, results, input, diagnostics, interactive)
	}
	// Output errors must enter cleanup, so a live explicit transaction rolls back.
	if finishErr := results.Finish(); e == nil {
		e = finishErr
	}
	e = client.Finish(ctx, e)
	if outputFile != nil {
		if closeErr := outputFile.Close(); closeErr != nil && e == nil {
			e = &sqlitecli.Error{Code: "OUTPUT_FAILED", Message: "output file close failed; SQL may have committed", Outcome: "failed", ExitCode: 6}
		}
	}
	return emit(e)
}

func sqliteTerminal(stream any) bool { return isTerminal(stream) }
