package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/webtemplate"
	"github.com/urfave/cli/v3"
)

func newWebTemplateCommands(output, diagnostics io.Writer) []*cli.Command {
	return append(newUnifiedTemplateCommands(output, diagnostics), []*cli.Command{
		{Name: "publish-share-template", Usage: "Publish a reviewed install.zip as a unified app template", ArgsUsage: "WEB_ID", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result"), &cli.BoolFlag{Name: "unified", Hidden: true, Local: true}, stringOption("template-id", "Update this stable template identity", ""), stringOption("request-id", "Stable request identity for publication/recovery", ""), stringOption("file", "Reviewed install.zip file", ""), &cli.BoolFlag{Name: "wait", Aliases: []string{"w"}, Usage: "Wait for publication to finish", Local: true}}, Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 || cmd.String("file") == "" {
				return argumentFailure(ctx, cmd, "Provide WEB_ID and --file install.zip")
			}
			if cmd.IsSet("template-id") && cmd.String("template-id") == "" {
				return argumentFailure(ctx, cmd, "--template-id must not be empty")
			}
			return publishUnifiedFromWeb(ctx, cmd, output, diagnostics)
		}},
		{Name: "status-share-template", Usage: "Look up unified templates created from a source Web application", ArgsUsage: "WEB_ID", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result")}, Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 {
				return argumentFailure(ctx, cmd, "Provide WEB_ID")
			}
			client, err := pinnedTemplateClient(ctx, diagnostics)
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			result, err := client.TemplatesBySource(ctx, cmd.Args().First())
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			return statusError(writeAppResult(apppublish.Success(result), cmd.Bool("json"), output, diagnostics))
		}},
		{Name: "init-share-template", Usage: "Download shared template source into a new project directory", Description: "Preserves source bytes, including declared template-files. Prepare those files in the new Web application's directory before ordinary Web publication; this command only downloads source.", ArgsUsage: "LINK", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result"), stringOption("dir", "New project directory", "")}, Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 || cmd.String("dir") == "" {
				return argumentFailure(ctx, cmd, "Provide LINK and --dir for a new project")
			}
			client, err := pinnedTemplateClient(ctx, diagnostics)
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			result, err := client.InitShare(ctx, cmd.Args().First(), cmd.String("dir"))
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			return statusError(writeAppResult(apppublish.Success(result), cmd.Bool("json"), output, diagnostics))
		}},
		{Name: "list-template", Usage: "Read the unified template market catalog", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result"), stringOption("after", "Next-page cursor", ""), stringOption("q", "Search query", ""), stringOption("sort", "Sort order: downloads or created_at", ""), &cli.Uint64Flag{Name: "category-id", Usage: "Filter by template category ID", Local: true}, &cli.IntFlag{Name: "limit", Usage: "Maximum items per page (1-100)", Local: true}}, Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 0 {
				return argumentFailure(ctx, cmd, "list-template does not accept positional arguments")
			}
			if cmd.IsSet("limit") && (cmd.Int("limit") < 1 || cmd.Int("limit") > 100) {
				return argumentFailure(ctx, cmd, "Use --limit between 1 and 100")
			}
			if cmd.IsSet("category-id") && cmd.Uint64("category-id") == 0 {
				return argumentFailure(ctx, cmd, "--category-id must be positive")
			}
			if s := cmd.String("sort"); s != "" && s != "downloads" && s != "created_at" {
				return argumentFailure(ctx, cmd, "--sort must be downloads or created_at")
			}
			client, err := pinnedTemplateClient(ctx, diagnostics)
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			page, err := client.ListCatalog(ctx, "market", cmd.String("after"), cmd.String("q"), cmd.String("sort"), cmd.Uint64("category-id"), int(cmd.Int("limit")))
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			return statusError(writeAppResult(apppublish.Success(page), cmd.Bool("json"), output, diagnostics))
		}},
		{Name: "init-template", Usage: "Download and extract a market template by template ID into a new directory", Description: "Preserves source bytes, including declared template-files. Prepare those files in the new Web application's directory before ordinary Web publication; Console template installation is a separate server workflow.", ArgsUsage: "TEMPLATE_ID", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result"), stringOption("version", "Fixed template version, if selected", ""), stringOption("dir", "New project directory", "")}, Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 || cmd.String("dir") == "" || !webtemplate.ValidTemplateID(cmd.Args().First()) || (cmd.String("version") != "" && (!webtemplate.ValidDecimal(cmd.String("version")) || cmd.String("version") == "0")) {
				return argumentFailure(ctx, cmd, "Provide TEMPLATE_ID and --dir for a new project")
			}
			client, err := pinnedTemplateClient(ctx, diagnostics)
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			result, err := client.InitUnifiedTemplate(ctx, cmd.Args().First(), "market", cmd.String("version"), cmd.String("dir"))
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
	}...)
}

func templateCommandFailure(err error, jsonMode bool, output, diagnostics io.Writer) error {
	e := &apppublish.Error{
		Code:       "WEB_TEMPLATE_FAILED",
		Message:    safeDisplay(err.Error()),
		NextAction: "Check login, template availability and the target directory. For uncertain writes, inspect the original request/job before another submission.",
		ExitCode:   1,
	}
	result := apppublish.Failure(e)
	var api *authclient.APIError
	if errors.As(err, &api) && api != nil {
		if requestID := safeDisplay(strings.TrimSpace(api.RequestID)); requestID != "" {
			e.RequestID = requestID
		}
		if apiCode := safeDisplay(strings.TrimSpace(api.Code)); apiCode != "" {
			result.Data = map[string]any{"api_code": apiCode}
			if msg := safeDisplay(strings.TrimSpace(api.Message)); msg != "" {
				e.Message = fmt.Sprintf("Template request failed (status %d, code %s): %s", api.Status, apiCode, msg)
			} else {
				e.Message = fmt.Sprintf("Template request failed (status %d, code %s)", api.Status, apiCode)
			}
			if apiCode == "TEMPLATE_SOURCE_UNAVAILABLE" {
				e.NextAction = "This template version was published without source (--without-source); it can be installed in Console, not cloned with init commands."
			}
		}
	}
	if !jsonMode {
		if data, ok := result.Data.(map[string]any); ok {
			if apiCode, ok := data["api_code"]; ok {
				fmt.Fprintf(diagnostics, "api_code: %s\n", safeDisplay(fmt.Sprint(apiCode)))
			}
		}
		if e.RequestID != "" {
			fmt.Fprintf(diagnostics, "request_id: %s\n", e.RequestID)
		}
	}
	return statusError(writeAppResult(result, jsonMode, output, diagnostics))
}
