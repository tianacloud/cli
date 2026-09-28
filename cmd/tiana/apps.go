package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/urfave/cli/v3"
)

func newAppCommand(input io.Reader, output, diagnostics io.Writer) *cli.Command {
	action := func(ctx context.Context, cmd *cli.Command) error {
		if cmd.NArg() != 1 {
			return argumentFailure(ctx, cmd, "Provide NAME for create, or ID for upload/status")
		}
		o := apppublish.Options{Command: cmd.Name, AppID: cmd.Args().First(), Dir: cmd.String("dir"), Version: cmd.String("version"), Entry: cmd.String("entry"), UploadCAFile: cmd.String("upload-ca-file")}
		if cmd.Name == "create" {
			o.Name, o.AppID = o.AppID, ""
			o.Description = cmd.String("description")
		}
		if err := o.Validate(); err != nil {
			return statusError(writeAppResult(apppublish.Failure(err), cmd.Bool("json"), output, diagnostics))
		}
		client, err := newAuthClient(ctx, diagnostics, true)
		if err != nil {
			fmt.Fprintln(diagnostics, "tiana: cannot initialize app publishing credentials")
			return statusError(1)
		}
		if o.Command == "create" {
			return statusError(executeAppCreate(ctx, client, o, cmd.Bool("json"), output, diagnostics))
		}
		result := (apppublish.Runner{Client: client}).Run(ctx, o)
		code := writeAppResult(result, cmd.Bool("json"), output, diagnostics)
		if ctx.Err() != nil {
			code = 130
		}
		return statusError(code)
	}
	common := func() []cli.Flag {
		return []cli.Flag{boolOption("json", "Write a structured JSON result")}
	}
	createFlags := append(common(), &cli.StringFlag{Name: "description", Aliases: []string{"m"}, Usage: "Application description (up to 1024 UTF-8 bytes)", Local: true})
	uploadFlags := append(common(), stringOption("dir", "Already-built static output directory", ""), stringOption("version", "Immutable version ID (defaults to content-derived ID)", ""), stringOption("entry", "Optional entry file relative to the output directory", ""), stringOption("upload-ca-file", "Additional trusted object-storage CA PEM", ""))
	statusFlags := append(common(), stringOption("version", "Version ID", ""))
	return &cli.Command{Name: "app", Aliases: []string{"apps"}, Usage: "App product commands", Action: groupAction, Commands: []*cli.Command{
		newAppServeCommand(output, diagnostics),
		newAppListCommand(input, output, diagnostics),
		newAppDeleteCommand(input, output, diagnostics),
		{Name: "create", Usage: "Create an application with a server-generated ID", ArgsUsage: "NAME", Flags: createFlags, Action: action},
		{Name: "upload", Usage: "Upload a build directory and publish a complete version", ArgsUsage: "ID", Description: "Preserves relative file paths within each App version. Repeating unchanged files resumes the same version; it does not build or host the app.", Flags: uploadFlags, Action: action},
		{Name: "status", Usage: "Inspect a version's upload and publication status", ArgsUsage: "ID", Flags: statusFlags, Action: action},
	}}
}

func writeAppResult(result apppublish.Result, jsonMode bool, output, diagnostics io.Writer) int {
	code := 0
	if result.Error != nil {
		code = result.Error.ExitCode
	}
	if jsonMode {
		if err := json.NewEncoder(output).Encode(result); err != nil {
			return 1
		}
		return code
	}
	if result.Error != nil {
		fmt.Fprintf(diagnostics, "tiana: %s\n%s\n", result.Error.Message, result.Error.NextAction)
		if data, ok := result.Data.(map[string]string); ok {
			if data["app_id"] != "" {
				fmt.Fprintf(diagnostics, "ID: %s\n", data["app_id"])
			}
			if data["version_id"] != "" {
				fmt.Fprintf(diagnostics, "Version: %s\n", data["version_id"])
			}
		}
		return code
	}
	bytes, err := json.MarshalIndent(result.Data, "", "  ")
	if err != nil {
		return 1
	}
	if _, err = fmt.Fprintln(output, string(bytes)); err != nil {
		return 1
	}
	return 0
}
