package main

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
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
		outputFile, err = os.OpenFile(o.output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			fmt.Fprintln(diagnostics, "tiana: cannot exclusively create output file")
			return 6
		}
		defer outputFile.Close()
		output = outputFile
	}
	var token *tiana.Token
	var e *sqlitecli.Error
	_, explicitToken := os.LookupEnv("TIANA_TOKEN")
	if explicitToken {
		token, e = sqlitecli.ReadTokenContext(ctx, "env", "TIANA_TOKEN", nil)
		if e != nil {
			return emit(e)
		}
	}
	resolution, err := resolve(ctx, o.reference, o.nonInteractive)
	if err != nil {
		if ctx.Err() != nil {
			return emit(&sqlitecli.Error{Code: "INTERRUPTED", Message: "interrupted before SQL was sent", Outcome: "not_sent", ExitCode: 130})
		}
		reportResolveError(diagnostics, o.reference, err)
		return 1
	}
	instance := resolution.instance
	endpoint, port, err := sqliteEndpoint(instance)
	if err != nil {
		fmt.Fprintln(diagnostics, "tiana:", safeDisplay(err.Error()))
		return 2
	}
	if !explicitToken {
		store, err := newInstanceTokenStore()
		if err != nil {
			fmt.Fprintln(diagnostics, "tiana: cannot open local InstanceToken store")
			return 2
		}
		endpointID := instance.EndpointID
		if endpointID == "" {
			endpointID = strings.SplitN(endpoint, ".", 2)[0]
		}
		session, err := resolution.client.CurrentSession(ctx)
		if err != nil {
			fmt.Fprintln(diagnostics, "tiana: cannot resolve current tenant")
			return 1
		}
		ids, err := store.CandidateIDs(session.TenantID, time.Now())
		if err != nil {
			fmt.Fprintln(diagnostics, "tiana:", safeDisplay(err.Error()))
			return 2
		}
		if resolution.client == nil {
			fmt.Fprintln(diagnostics, "tiana: cannot authorize local InstanceToken candidates")
			return 2
		}
		eligible, err := resolution.client.CredentialCandidates(ctx, instance.ID, endpointID, ids)
		if err != nil {
			fmt.Fprintln(diagnostics, "tiana: cannot authorize local InstanceToken candidates")
			return 1
		}
		credential, err := store.LookupCandidates(session.TenantID, eligible, time.Now())
		if err != nil {
			if errors.Is(err, authclient.ErrInstanceTokenNotFound) {
				command := "tiana sqlite tokens create " + quoteCommandArgs([]string{instance.ID})
				if o.branch != "" {
					command += " --branch " + quoteCommandArgs([]string{o.branch})
				}
				fmt.Fprintf(diagnostics, "tiana: no usable local InstanceToken; run %s or set TIANA_TOKEN\n", command)
			} else {
				fmt.Fprintln(diagnostics, "tiana:", safeDisplay(err.Error()))
			}
			return 2
		}
		token, err = tiana.NewToken(credential.Token)
		if err != nil {
			fmt.Fprintln(diagnostics, "tiana: invalid saved InstanceToken; create a replacement explicitly")
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
