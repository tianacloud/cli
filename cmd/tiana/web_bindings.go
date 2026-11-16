package main

import (
	"context"
	"io"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/urfave/cli/v3"
)

func newWebBindingsCommand(output, diagnostics io.Writer) *cli.Command {
	action := func(ctx context.Context, cmd *cli.Command) error {
		if cmd.NArg() != 1 {
			return argumentFailure(ctx, cmd, "Provide WEB_ID")
		}
		var update *apppublish.WebBindingsUpdate
		if cmd.Name != "get" {
			update = &apppublish.WebBindingsUpdate{ExpectedRevision: cmd.Uint64("expected-revision")}
			if cmd.Name == "bind" {
				if cmd.IsSet("database-instance-id") {
					id := cmd.String("database-instance-id")
					if id == "" {
						return argumentFailure(ctx, cmd, "Provide a SQLite instance ID; use bindings unbind to remove it")
					}
					update.DatabaseInstanceID = &id
				}
				if cmd.IsSet("git-instance-id") {
					id := cmd.String("git-instance-id")
					if id == "" {
						return argumentFailure(ctx, cmd, "Provide a Git instance ID; use bindings unbind to remove it")
					}
					update.GitInstanceID = &id
				}
			} else {
				empty := ""
				if cmd.Bool("database") {
					update.DatabaseInstanceID = &empty
				}
				if cmd.Bool("git") {
					update.GitInstanceID = &empty
				}
			}
			if update.ExpectedRevision == 0 || (update.DatabaseInstanceID == nil && update.GitInstanceID == nil) {
				return argumentFailure(ctx, cmd, "Select SQLite or Git and provide --expected-revision from bindings get")
			}
			release, err := lockManagementMutation(ctx, diagnostics)
			if err != nil {
				writeCommandError(diagnostics, err)
				return statusError(1)
			}
			defer release()
		}
		client, err := newAuthClient(ctx, diagnostics, cmd.Bool("json"))
		if err != nil {
			writeCommandError(diagnostics, err)
			return statusError(1)
		}
		runner, e := (apppublish.Runner{Client: client}).Bind(ctx)
		if e != nil {
			return statusError(writeAppResult(apppublish.Failure(e), cmd.Bool("json"), output, diagnostics))
		}
		return statusError(writeAppResult(runner.Bindings(ctx, cmd.Args().First(), update), cmd.Bool("json"), output, diagnostics))
	}
	common := func() []cli.Flag {
		return []cli.Flag{functionJSONFlag(), &cli.Uint64Flag{Name: "expected-revision", Usage: "Project revision returned by bindings get or the previous successful change", Local: true}}
	}
	return &cli.Command{Name: "bindings", Usage: "Inspect, add or remove SQLite and Git bindings", Action: groupAction, Commands: []*cli.Command{
		{Name: "get", ArgsUsage: "WEB_ID", Flags: []cli.Flag{functionJSONFlag()}, Action: action},
		{Name: "bind", ArgsUsage: "WEB_ID", Usage: "Bind an existing instance to an unbound resource", Flags: append(common(),
			&cli.StringFlag{Name: "database-instance-id", Usage: "SQLite instance ID", Local: true},
			&cli.StringFlag{Name: "git-instance-id", Usage: "Git instance ID", Local: true}), Action: action},
		{Name: "unbind", ArgsUsage: "WEB_ID", Usage: "Remove selected bindings while retaining their instances and data", Flags: append(common(),
			&cli.BoolFlag{Name: "database", Usage: "Remove the SQLite binding", Local: true},
			&cli.BoolFlag{Name: "git", Usage: "Remove the Git binding", Local: true}), Action: action},
	}}
}
