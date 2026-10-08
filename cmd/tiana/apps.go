package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/urfave/cli/v3"
)

func newWebCommand(input io.Reader, output, diagnostics io.Writer) *cli.Command {
	action := func(ctx context.Context, cmd *cli.Command) error {
		if cmd.NArg() != 1 {
			return argumentFailure(ctx, cmd, "Provide NAME for create, or ID for publish/status")
		}
		o := apppublish.Options{Command: cmd.Name, ID: cmd.Args().First(), Dir: cmd.String("dir"), PublishID: cmd.String("publish-id")}
		if cmd.Name == "create" {
			o.Name, o.ID = o.ID, ""
			o.Description = cmd.String("description")
			o.WebProjectParams = apppublish.WebProjectParams{Entry: cmd.String("entry"), DatabaseInstanceID: cmd.String("database-instance-id"), GitInstanceID: cmd.String("git-instance-id")}
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
			return statusError(executeWebCreate(ctx, client, o, cmd.Bool("json"), output, diagnostics))
		}
		result := (apppublish.Runner{Client: client, PersistPublication: persistWebPublication, OnPublishID: func(id string) { fmt.Fprintln(diagnostics, "Publish ID:", id) }}).Run(ctx, o)
		code := writeAppResult(result, cmd.Bool("json"), output, diagnostics)
		if ctx.Err() != nil {
			code = 130
		}
		return statusError(code)
	}
	common := func() []cli.Flag {
		return []cli.Flag{boolOption("json", "Write a structured JSON result")}
	}
	createFlags := append(common(), &cli.StringFlag{Name: "description", Aliases: []string{"m"}, Usage: "Application description (up to 2048 UTF-8 bytes)", Local: true})
	createFlags = append(createFlags, stringOption("entry", "Create-only relative .js/.mjs Site entry (omit for file hosting)", ""), stringOption("database-instance-id", "Existing SQLite instance binding", ""), stringOption("git-instance-id", "Existing Git instance binding", ""))
	publishFlags := append(common(), stringOption("dir", "Output directory to package as one .tweb archive", ""))
	statusFlags := append(common(), stringOption("publish-id", "Query a bounded in-memory publication receipt", ""))
	return &cli.Command{Name: "web", Usage: "Web product commands", Action: groupAction, Commands: append([]*cli.Command{
		newWebServeCommand(output, diagnostics),
		newWebListCommand(input, output, diagnostics),
		newWebDeleteCommand(input, output, diagnostics),
		{Name: "create", Usage: "Create a Web application with a server-generated ID", ArgsUsage: "NAME", Flags: createFlags, Action: action},
		{Name: "publish", Usage: "Package a directory and upload through the Web control channel", ArgsUsage: "ID", Description: "Overwrites the single current archive; returns after confirmed S3 upload while activation continues.", Flags: publishFlags, Action: action},
		{Name: "status", Usage: "Inspect current content or publication activation", ArgsUsage: "ID", Flags: statusFlags, Action: action},
	}, newWebTemplateCommands(output, diagnostics)...)}
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
			if data["id"] != "" {
				fmt.Fprintf(diagnostics, "ID: %s\n", data["id"])
			}
			if data["publish_id"] != "" {
				fmt.Fprintf(diagnostics, "Publish ID: %s\n", data["publish_id"])
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
