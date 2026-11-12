package main

import (
	"context"
	"io"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/urfave/cli/v3"
)

func newWebShareCommand(output, diagnostics io.Writer) *cli.Command {
	action := func(ctx context.Context, cmd *cli.Command) error {
		if cmd.NArg() != 1 {
			return argumentFailure(ctx, cmd, "Provide WEB_ID")
		}
		var update *apppublish.WebShareUpdate
		if cmd.Name == "set" {
			if cmd.IsSet("enabled") == cmd.IsSet("auto-approve") || !cmd.IsSet("expected-revision") {
				return argumentFailure(ctx, cmd, "Set exactly one of --enabled or --auto-approve and provide --expected-revision from share get")
			}
			release, err := lockManagementMutation(ctx, diagnostics)
			if err != nil {
				writeCommandError(diagnostics, err)
				return statusError(1)
			}
			defer release()
			update = &apppublish.WebShareUpdate{ExpectedRevision: cmd.Uint64("expected-revision")}
			value := cmd.Bool("enabled")
			if cmd.IsSet("enabled") {
				update.Enabled = &value
			} else {
				value = cmd.Bool("auto-approve")
				update.AutoApprove = &value
			}
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
		return statusError(writeAppResult(runner.Share(ctx, cmd.Args().First(), update), cmd.Bool("json"), output, diagnostics))
	}
	return &cli.Command{Name: "share", Usage: "Inspect or change invitations to use this application", Action: groupAction, Commands: []*cli.Command{
		{Name: "get", ArgsUsage: "WEB_ID", Flags: []cli.Flag{functionJSONFlag()}, Action: action},
		{Name: "set", ArgsUsage: "WEB_ID", Usage: "Change one sharing setting; auto-approval applies to new requests", Flags: []cli.Flag{
			functionJSONFlag(),
			&cli.BoolFlag{Name: "enabled", Usage: "Enable or disable new invitations (--enabled=true/false)", Local: true},
			&cli.BoolFlag{Name: "auto-approve", Usage: "Automatically approve new requests (--auto-approve=true/false); existing pending requests remain pending", Local: true},
			&cli.Uint64Flag{Name: "expected-revision", Usage: "Project revision returned by share get or the previous successful set", Local: true},
		}, Action: action},
	}}
}
