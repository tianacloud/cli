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
		{Name: "init-template", Usage: "Download and extract a Web template into a new directory", ArgsUsage: "NAME", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result"), stringOption("dir", "New project directory", "")}, Action: func(ctx context.Context, cmd *cli.Command) error {
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
