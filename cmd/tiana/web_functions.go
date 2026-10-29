package main

import (
	"context"
	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/urfave/cli/v3"
	"io"
)

func newWebFunctionsCommand(output, diagnostics io.Writer) *cli.Command {
	action := func(ctx context.Context, cmd *cli.Command) error {
		if cmd.NArg() != 1 || (cmd.Name == "set" && cmd.Uint64("expected-revision") == 0) {
			return argumentFailure(ctx, cmd, "Provide WEB_ID; set also requires --expected-revision from functions get")
		}
		if cmd.Name == "set" {
			release, err := lockFunctionMutation(ctx, diagnostics)
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
		var update *apppublish.WebFunctionsUpdate
		if cmd.Name == "set" {
			ids := cmd.StringSlice("function-id")
			if ids == nil {
				ids = []string{}
			}
			update = &apppublish.WebFunctionsUpdate{FunctionInstanceIDs: ids, ExpectedRevision: cmd.Uint64("expected-revision")}
		}
		result, e := runner.Functions(ctx, cmd.Args().First(), update)
		if e != nil {
			return statusError(writeAppResult(apppublish.Failure(e), cmd.Bool("json"), output, diagnostics))
		}
		return statusError(writeAppResult(apppublish.Success(result), cmd.Bool("json"), output, diagnostics))
	}
	return &cli.Command{Name: "functions", Usage: "Inspect or replace the Function backends available to a Web app", Action: groupAction, Commands: []*cli.Command{
		{Name: "get", ArgsUsage: "WEB_ID", Flags: []cli.Flag{functionJSONFlag()}, Action: action},
		{Name: "set", ArgsUsage: "WEB_ID", Usage: "Replace bindings; omit --function-id to clear all", Flags: []cli.Flag{functionJSONFlag(), &cli.StringSliceFlag{Name: "function-id", Usage: "Bound Function ID (repeat for multiple functions)", Local: true}, &cli.Uint64Flag{Name: "expected-revision", Usage: "Project revision returned by functions get", Local: true}}, Action: action},
	}}
}
