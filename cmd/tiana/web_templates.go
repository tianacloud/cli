package main

import (
	"context"
	"fmt"
	"io"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/webtemplate"
	"github.com/urfave/cli/v3"
)

func newWebTemplateCommands(output, diagnostics io.Writer) []*cli.Command {
	return []*cli.Command{
		{Name: "publish-share-template", Usage: "Publish a reviewed install.zip as an app template", ArgsUsage: "WEB_ID", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result"), stringOption("file", "Reviewed install.zip file", ""), &cli.BoolFlag{Name: "wait", Aliases: []string{"w"}, Usage: "Wait for publication to finish", Local: true}}, Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 || cmd.String("file") == "" {
				return argumentFailure(ctx, cmd, "Provide WEB_ID and --file install.zip")
			}
			auth, err := newAuthClient(ctx, diagnostics, true)
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			client := webtemplate.Client{Auth: auth}
			result, err := client.PublishShare(ctx, cmd.Args().First(), cmd.String("file"))
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			var value any = result
			if cmd.Bool("wait") {
				value, err = client.WaitShare(ctx, cmd.Args().First(), result.JobID)
				if err != nil {
					return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
				}
			}
			return statusError(writeAppResult(apppublish.Success(value), cmd.Bool("json"), output, diagnostics))
		}},
		{Name: "status-share-template", Usage: "Read the current share, publication and cleanup status", ArgsUsage: "WEB_ID", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result")}, Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 {
				return argumentFailure(ctx, cmd, "Provide WEB_ID")
			}
			auth, err := newAuthClient(ctx, diagnostics, true)
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			result, err := (webtemplate.Client{Auth: auth}).ShareStatus(ctx, cmd.Args().First())
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			return statusError(writeAppResult(apppublish.Success(result), cmd.Bool("json"), output, diagnostics))
		}},
		{Name: "withdraw-share-template", Usage: "Withdraw the current template from new installations", ArgsUsage: "WEB_ID", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result")}, Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 {
				return argumentFailure(ctx, cmd, "Provide WEB_ID")
			}
			auth, err := newAuthClient(ctx, diagnostics, true)
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			id, err := (webtemplate.Client{Auth: auth}).WithdrawShare(ctx, cmd.Args().First())
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			return statusError(writeAppResult(apppublish.Success(map[string]any{"cleanup_job_id": id}), cmd.Bool("json"), output, diagnostics))
		}},
		{Name: "init-share-template", Usage: "Download shared source into a new project directory", Description: "Preserves source bytes, including declared template-files. Prepare those files in the new Web application's directory before ordinary Web publication; this command only downloads source.", ArgsUsage: "LINK", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result"), stringOption("dir", "New project directory", "")}, Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 || cmd.String("dir") == "" {
				return argumentFailure(ctx, cmd, "Provide LINK and --dir for a new project")
			}
			auth, err := newAuthClient(ctx, diagnostics, true)
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			result, err := (webtemplate.Client{Auth: auth}).InitShare(ctx, cmd.Args().First(), cmd.String("dir"))
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			return statusError(writeAppResult(apppublish.Success(result), cmd.Bool("json"), output, diagnostics))
		}},
		{Name: "list-template", Usage: "Read the current Web template catalog", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result"), stringOption("after", "Next-page cursor", "")}, Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 0 {
				return argumentFailure(ctx, cmd, "list-template does not accept positional arguments")
			}
			client, err := newAuthClient(ctx, diagnostics, true)
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			page, err := (webtemplate.Client{Auth: client}).List(ctx, cmd.String("after"))
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			return statusError(writeAppResult(apppublish.Success(page), cmd.Bool("json"), output, diagnostics))
		}},
		{Name: "init-template", Usage: "Download and extract a Web template into a new directory", Description: "Preserves source bytes, including declared template-files. Prepare those files in the new Web application's directory before ordinary Web publication; Console template installation is a separate server workflow.", ArgsUsage: "NAME", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result"), stringOption("dir", "New project directory", "")}, Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 || cmd.String("dir") == "" {
				return argumentFailure(ctx, cmd, "Provide a template NAME and --dir for a new project")
			}
			client, err := newAuthClient(ctx, diagnostics, true)
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			result, err := (webtemplate.Client{Auth: client}).Init(ctx, cmd.Args().First(), cmd.String("dir"))
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			if cmd.Bool("json") {
				code := writeAppResult(apppublish.Success(result), true, output, diagnostics)
				if code != 0 {
					fmt.Fprintf(diagnostics, "Project created at %s; inspect metadata.json before continuing.\n", result.Directory)
				}
				return statusError(code)
			}
			if _, err := fmt.Fprintf(output, "Project: %s\nMetadata: %s\nNext: read %s/README.md and fill metadata.json variables.\n", result.Directory, result.MetadataPath, result.Directory); err != nil {
				fmt.Fprintf(diagnostics, "Project created at %s; inspect metadata.json before continuing.\n", result.Directory)
				return statusError(1)
			}
			return nil
		}},
	}
}

func templateCommandFailure(err error, jsonMode bool, output, diagnostics io.Writer) error {
	e := &apppublish.Error{Code: "WEB_TEMPLATE_FAILED", Message: err.Error(), NextAction: "Check login, template availability and the target directory, then retry.", ExitCode: 1}
	return statusError(writeAppResult(apppublish.Failure(e), jsonMode, output, diagnostics))
}
