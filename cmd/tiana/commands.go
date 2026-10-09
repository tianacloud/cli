package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/clientconfig"
	requestdiag "github.com/tianacloud/cli/internal/diagnostics"
	"github.com/tianacloud/cli/internal/sqlitecli"
	"github.com/tianacloud/cli/internal/updatecheck"
	"github.com/urfave/cli/v3"
)

type commandStatus struct{ code int }

func (e *commandStatus) Error() string { return "command failed" }
func statusError(code int) error {
	if code == 0 {
		return nil
	}
	return &commandStatus{code: code}
}

type sqlCommandAction func(context.Context, sqliteOptions) int

// urfave snapshots this variable at package initialization and can log raw argv.
// Refuse the opt-in tracing mode before invoking any framework method.
var frameworkTracingAtStartup = os.Getenv("URFAVE_CLI_TRACING") == "on"

func runCLI(ctx context.Context, args []string, input io.Reader, output, diagnostics io.Writer) int {
	return runCLIWithSQL(ctx, args, input, output, diagnostics, nil)
}

func runCLIWithSQL(ctx context.Context, args []string, input io.Reader, output, diagnostics io.Writer, sqlAction sqlCommandAction) int {
	var requestIDs bytes.Buffer
	if os.Getenv("TIANA_DIAGNOSTICS") == "1" {
		ctx = requestdiag.WithWriter(ctx, diagnostics)
	} else {
		ctx = requestdiag.WithWriter(ctx, &requestIDs)
	}
	if frameworkTracingAtStartup || os.Getenv("URFAVE_CLI_TRACING") == "on" {
		fmt.Fprintln(diagnostics, "tiana: unset URFAVE_CLI_TRACING to avoid exposing SQL or credentials")
		return 2
	}
	// db is deliberately not registered, including as a hidden alias. Reject
	// its old --help form as well, without parsing or echoing the trailing args.
	if len(args) > 0 && args[0] == "db" {
		fmt.Fprintln(diagnostics, "tiana: db has been removed; use tiana sqlite create/list/show for SQLite instances")
		return 2
	}
	cmd := newCLICommand(input, output, diagnostics, sqlAction)
	var machine *countedJSONWriter
	if requestedManagementJSON(cmd, args) {
		ctx = context.WithValue(ctx, managementJSONKey{}, true)
		machine = &countedJSONWriter{Writer: output}
		cmd = newCLICommand(input, machine, diagnostics, sqlAction)
	}
	err := cmd.Run(ctx, append([]string{"tiana"}, args...))
	if err == nil {
		passiveUpdate(ctx, args, input, output, diagnostics)
		return 0
	}
	_, _ = io.Copy(diagnostics, &requestIDs)
	var status *commandStatus
	if errors.As(err, &status) {
		if machine != nil && machine.written == 0 {
			failure := &apppublish.Error{Code: "MANAGEMENT_FAILED", Message: "Command did not complete", ExitCode: status.code}
			if status.code == 2 {
				failure.Code = "INVALID_INPUT"
				failure.Message = "Invalid command options or arguments"
			}
			_ = writeAppResult(apppublish.Failure(failure), true, machine, diagnostics)
		}
		if status.code == 1 && ctx.Err() != nil {
			return 130
		}
		return status.code
	}
	// Parser errors can contain user values (including accidental Tokens).
	// Never forward arbitrary framework errors to diagnostics.
	fmt.Fprintln(diagnostics, "tiana: invalid command or arguments; use tiana --help")
	return 2
}

func showHelp(ctx context.Context, cmd *cli.Command) error {
	lineage := cmd.Lineage()
	if len(lineage) == 1 {
		return cli.ShowRootCommandHelp(cmd)
	}
	return cli.ShowCommandHelp(ctx, lineage[1], cmd.Name)
}

func argumentFailure(ctx context.Context, cmd *cli.Command, message string) error {
	if managementJSON(ctx) {
		return statusError(writeAppResult(apppublish.Failure(&apppublish.Error{Code: "INVALID_INPUT", Message: message, ExitCode: 2}), true, cmd.Root().Writer, cmd.Root().ErrWriter))
	}
	fmt.Fprintln(cmd.Root().ErrWriter, "tiana:", message)
	writer := cmd.Root().Writer
	cmd.Root().Writer = cmd.Root().ErrWriter
	defer func() { cmd.Root().Writer = writer }()
	_ = showHelp(ctx, cmd)
	return statusError(2)
}

func groupAction(ctx context.Context, cmd *cli.Command) error {
	if cmd.NArg() != 0 {
		return argumentFailure(ctx, cmd, "unknown subcommand")
	}
	return showHelp(ctx, cmd)
}

// Keep the exact pre-parsed leaf argv for existing pending-operation matching.
// urfave leaves a child's entire argument vector intact on its parent.
func leafArguments(cmd *cli.Command) []string {
	return append([]string(nil), cmd.Lineage()[1].Args().Tail()...)
}

// urfave trims ordinary positional arguments. Never silently change a resource
// name (or a retried create payload). A -- separator keeps
// significant surrounding whitespace intact without a second argv parser.
func positionalWasTrimmed(cmd *cli.Command, value string) bool {
	for _, raw := range leafArguments(cmd) {
		if raw != value && strings.TrimSpace(raw) == value {
			return true
		}
	}
	return false
}

func stringOption(name, usage, value string) cli.Flag {
	return &cli.StringFlag{Name: name, Usage: usage, Value: value, Local: true}
}
func boolOption(name, usage string) cli.Flag {
	return &cli.BoolFlag{Name: name, Usage: usage, Local: true}
}

func newCLICommand(input io.Reader, output, diagnostics io.Writer, sqlAction sqlCommandAction) *cli.Command {
	if sqlAction == nil {
		sqlAction = func(ctx context.Context, o sqliteOptions) int {
			return executeSQLite(ctx, o, input, output, diagnostics,
				func(ctx context.Context, ref string, nonInteractive bool) (sqliteResolution, error) {
					return resolveSQLite(ctx, ref, o.branch, nonInteractive, diagnostics)
				}, nil)
		}
	}
	noArgs := func(run func(context.Context, io.Writer, io.Writer) int) cli.ActionFunc {
		return func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 0 {
				return argumentFailure(ctx, cmd, "this command does not accept arguments")
			}
			return statusError(run(ctx, output, diagnostics))
		}
	}
	root := &cli.Command{
		Name: "tiana", Usage: "Tiana account, Web, SQLite and Git commands",
		Reader: input, Writer: output, ErrWriter: diagnostics, HideVersion: true,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "ca-file", Usage: "Deployment CA PEM for all MGR and Gateway connections"},
		},
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Action:         groupAction,
		Commands: []*cli.Command{
			newVersionCommand(output, diagnostics, updatecheck.Checker{}),
			newLoginCommand(output, diagnostics),
			{Name: "logout", Usage: "Sign out and clear local account credentials", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result")}, Action: noArgs(runLogout)},
			{Name: "status", Usage: "Show login status and tenant quota", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result")}, Description: "Shows the signed-in account and tenant usage/limits without starting browser login. Unavailable login or quota returns a nonzero exit status.", Action: noArgs(runStatus)},

			newSQLiteCommand(input, output, diagnostics, sqlAction),
			{Name: "connect", Usage: "Connect a native client through the helper", ArgsUsage: "[options] -- <native client> [args...]", SkipFlagParsing: true,
				Description: "Arguments after -- belong to the native client and are passed unchanged.",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return statusError(runConnect(ctx, append([]string{"connect"}, cmd.Args().Slice()...), output, diagnostics))
				}},
			newGitCommand(input, output, diagnostics),
			newWebCommand(input, output, diagnostics),
		},
	}
	for _, command := range root.Commands {
		switch command.Name {
		case "web", "sqlite", "git":
			command.Category = "Products"
		}
	}
	configureCommandErrors(root)
	before := root.Before
	root.Before = func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
		ctx, err := before(ctx, cmd)
		if err != nil {
			return ctx, err
		}
		path := cmd.String("ca-file")
		if !cmd.IsSet("ca-file") {
			path = os.Getenv("TIANA_CA_FILE")
		}
		if cmd.IsSet("ca-file") && path == "" {
			return ctx, argumentFailure(ctx, cmd, "--ca-file requires a non-empty path")
		}
		trust, err := clientconfig.Load(path)
		if err != nil {
			fmt.Fprintln(diagnostics, "tiana:", safeDisplay(err.Error()))
			return ctx, statusError(2)
		}
		ctx = clientconfig.WithTrust(ctx, trust)
		return ctx, nil
	}
	return root
}

func configureCommandErrors(cmd *cli.Command) {
	if len(cmd.Commands) == 0 && !cmd.SkipFlagParsing {
		// v3.11 treats a leaf's positional argument as a help topic when
		// --help follows it. The topic is a resource, not another command.
		cmd.CommandNotFound = func(ctx context.Context, cmd *cli.Command, _ string) {
			_ = showHelp(ctx, cmd)
		}
	}
	cmd.OnUsageError = func(ctx context.Context, cmd *cli.Command, _ error, _ bool) error {
		return argumentFailure(ctx, cmd, "invalid options or option value")
	}
	cmd.Before = func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
		for _, flag := range cmd.Flags {
			if counter, ok := flag.(cli.Countable); ok && counter.Count() > 1 {
				return ctx, argumentFailure(ctx, cmd, "an option was provided more than once")
			}
		}
		return ctx, nil
	}
	for _, child := range cmd.Commands {
		configureCommandErrors(child)
	}
}

func newSQLiteCommand(input io.Reader, output, diagnostics io.Writer, sqlAction sqlCommandAction) *cli.Command {
	create := &cli.Command{Name: "create", Usage: "Submit SQLite instance creation", ArgsUsage: "NAME",
		Flags:       []cli.Flag{createWaitOption(), createMessageOption(), boolOption("json", "Write a structured JSON result")},
		Description: "NAME is required as a positional argument. The sqlite engine is fixed. Returns after acceptance by default; -w/--wait waits for creation success. Never creates a Token.",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() > 1 {
				return argumentFailure(ctx, cmd, "the database name was provided more than once")
			}
			name := cmd.Args().First()
			if instanceCreateNameWasTrimmed(cmd, name) {
				return argumentFailure(ctx, cmd, "use -- to preserve surrounding whitespace; resume older pending operations with the older CLI")
			}
			if strings.TrimSpace(name) == "" {
				return argumentFailure(ctx, cmd, "a database name is required")
			}
			o := createOptions{json: cmd.Bool("json"), wait: cmd.Bool("wait"), input: authclient.CreateInstanceRequest{DisplayName: name, Notes: cmd.String("message"), Engine: "sqlite", Config: map[string]interface{}{}}, nonInteractive: false}
			return statusError(executeSQLiteCreate(ctx, o, leafArguments(cmd), output, diagnostics))
		}}
	list := &cli.Command{Name: "list", Usage: "List SQLite instances only", Description: "Fetch all pages and display every matching instance without prompting.", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result")},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 0 {
				return argumentFailure(ctx, cmd, "list does not accept arguments")
			}
			return statusError(executeSQLiteList(ctx, listOptions{nonInteractive: false}, input, output, diagnostics))
		}}
	show := &cli.Command{Name: "show", Usage: "Show a SQLite instance", ArgsUsage: "INSTANCE", Description: "With --url, stdout contains only the connection URL. --url and --json are mutually exclusive.", Flags: []cli.Flag{boolOption("url", "Print only the connection URL"), boolOption("json", "Write a structured JSON result"), branchOption()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Bool("json") && cmd.Bool("url") {
				return argumentFailure(ctx, cmd, "--json and --url are mutually exclusive")
			}
			if cmd.NArg() != 1 || strings.TrimSpace(cmd.Args().First()) == "" {
				return argumentFailure(ctx, cmd, "one instance ID or name is required")
			}
			if positionalWasTrimmed(cmd, cmd.Args().First()) {
				return argumentFailure(ctx, cmd, "use -- before the instance name to preserve surrounding whitespace")
			}
			return statusError(executeSQLiteShow(ctx, showOptions{reference: cmd.Args().First(), branch: cmd.String("branch"), urlOnly: cmd.Bool("url"), nonInteractive: false}, output, diagnostics))
		}}
	branches := &cli.Command{Name: "branch", Usage: "Manage SQLite branches", Action: groupAction, Commands: []*cli.Command{
		newBranchMutationCommand("create", input, output, diagnostics),
		newBranchMutationCommand("delete", input, output, diagnostics),
		{Name: "list", Usage: "List all matching branches", ArgsUsage: "INSTANCE", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result"), stringOption("after", "Start after this branch cursor", ""), stringOption("search", "Filter branch names by substring", "")}, Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 || strings.TrimSpace(cmd.Args().First()) == "" {
				return argumentFailure(ctx, cmd, "one instance ID or name is required")
			}
			return statusError(executeSQLiteBranchesList(ctx, cmd.Args().First(), cmd.String("after"), cmd.String("search"), output, diagnostics))
		}},
	}}
	commands := []*cli.Command{create, list, show, newInstanceDeleteCommand(input, output, diagnostics, sqliteManagementScope), branches, newSQLCommand(sqlAction)}
	return &cli.Command{Name: "sqlite", Usage: "SQLite product commands", Description: "Use an MGR instance ID, not an ep-... Endpoint ID. Name lookup requires MGR display_name support. SQL uses native sdk-go with verified TLS; no SQL replay. --atomic is unavailable.", Action: groupAction, Commands: commands}
}

func newSQLCommand(action sqlCommandAction) *cli.Command {
	flags := []cli.Flag{
		stringOption("endpoint", "Connect directly using an HTTPS Endpoint URL or hostname[:port]; uses TIANA_TOKEN/TIANA_TOKEN_FILE or account login", ""),
		branchOption(),
		stringOption("format", "table, json, ndjson or csv", "table"),
		boolOption("json", "Alias for --format json with non-interactive SQL"),
		stringOption("output", "Exclusively create a private result file", ""),
		&cli.UintFlag{Name: "timeout", Usage: "Per-request timeout in milliseconds (1..3600000)", Value: 30000, Local: true},
		&cli.StringFlag{Name: "execute", Aliases: []string{"e"}, Usage: "Execute one SQL statement and exit", Local: true},
		&cli.StringFlag{Name: "file", Aliases: []string{"f"}, Usage: "Preflight and execute a SQL script, then exit", Local: true},
		&cli.BoolFlag{Name: "atomic", Hidden: true, Local: true},
	}
	return &cli.Command{Name: "shell", Usage: "Open the SQLite shell, execute SQL (-e), or run a script (-f)", ArgsUsage: "[INSTANCE | --endpoint ENDPOINT]",
		Description: "Resolve INSTANCE through MGR, or use --endpoint to bypass MGR. Direct mode cannot use INSTANCE or --branch. Use TIANA_TOKEN or TIANA_TOKEN_FILE when set; otherwise use the logged-in account access token. Missing login is an error.",
		Flags:       flags, Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Bool("json") {
				if cmd.IsSet("format") && cmd.String("format") != "json" {
					return argumentFailure(ctx, cmd, "--json conflicts with the selected --format")
				}
				if !cmd.IsSet("execute") && !cmd.IsSet("file") && isTerminal(cmd.Root().Reader) {
					return argumentFailure(ctx, cmd, "--json requires -e, -f or piped SQL input")
				}
			}
			if cmd.IsSet("atomic") {
				return argumentFailure(ctx, cmd, "--atomic is unavailable until SQLite grammar equivalence is verified")
			}
			var endpoint, port string
			if cmd.IsSet("endpoint") {
				if cmd.NArg() != 0 || cmd.IsSet("branch") {
					return argumentFailure(ctx, cmd, "--endpoint cannot be combined with INSTANCE or --branch")
				}
				var err error
				endpoint, port, err = parseSQLiteDirectEndpoint(cmd.String("endpoint"))
				if err != nil {
					return argumentFailure(ctx, cmd, err.Error())
				}
			} else {
				if cmd.NArg() != 1 || strings.TrimSpace(cmd.Args().First()) == "" {
					return argumentFailure(ctx, cmd, "one instance ID or name, or --endpoint, is required")
				}
				if positionalWasTrimmed(cmd, cmd.Args().First()) {
					return argumentFailure(ctx, cmd, "use -- before the instance name to preserve surrounding whitespace")
				}
			}
			if !sqlitecli.ValidFormat(cmd.String("format")) || cmd.Uint("timeout") < 1 || cmd.Uint("timeout") > 3600000 {
				return argumentFailure(ctx, cmd, "invalid SQL format or timeout")
			}
			if cmd.IsSet("execute") && cmd.IsSet("file") {
				return argumentFailure(ctx, cmd, "-e and -f are mutually exclusive")
			}
			for _, key := range []string{"execute", "file", "output"} {
				if cmd.IsSet(key) && cmd.String(key) == "" {
					return argumentFailure(ctx, cmd, "option value must not be empty")
				}
			}
			o := sqliteOptions{command: "shell", endpoint: endpoint, port: port, reference: cmd.Args().First(), branch: cmd.String("branch"), format: cmd.String("format"), output: cmd.String("output"), timeout: time.Duration(cmd.Uint("timeout")) * time.Millisecond}
			if cmd.Bool("json") {
				o.format = "json"
			}
			if cmd.IsSet("execute") {
				o.command = "exec"
				o.sql = cmd.String("execute")
			}
			if cmd.IsSet("file") {
				o.command = "run"
				o.file = cmd.String("file")
			}
			return statusError(action(ctx, o))
		}}
}

func branchOption() cli.Flag {
	return stringOption("branch", "Exact branch name; omitted selects the default branch (ID main)", "")
}
