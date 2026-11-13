package main

import (
	"context"
	"io"
	"os"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/webtemplate"
	"github.com/urfave/cli/v3"
)

func newUnifiedTemplateCommands(output, diagnostics io.Writer) []*cli.Command {
	return []*cli.Command{
		{Name: "init-unified-template", Usage: "Download source by template ID (candidate API; requires matching MGR)", ArgsUsage: "TEMPLATE_ID", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result"), stringOption("channel", "share or market", "market"), stringOption("version", "Fixed template version, if selected", ""), stringOption("dir", "New project directory", "")}, Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 || cmd.String("dir") == "" {
				return argumentFailure(ctx, cmd, "Provide TEMPLATE_ID and --dir")
			}
			client, err := pinnedTemplateClient(ctx, diagnostics)
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			result, err := client.InitUnifiedTemplate(ctx, cmd.Args().First(), cmd.String("channel"), cmd.String("version"), cmd.String("dir"))
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			return statusError(writeAppResult(apppublish.Success(result), cmd.Bool("json"), output, diagnostics))
		}},
		{Name: "list-owned-templates", Usage: "Read one page of space templates (candidate API; requires matching MGR)", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result"), stringOption("after", "Opaque next-page cursor", ""), &cli.IntFlag{Name: "limit", Value: 20, Local: true}}, Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 0 || cmd.Int("limit") < 1 || cmd.Int("limit") > 100 {
				return argumentFailure(ctx, cmd, "Use --limit between 1 and 100 without positional arguments")
			}
			client, err := pinnedTemplateClient(ctx, diagnostics)
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			result, err := client.ListTemplates(ctx, cmd.String("after"), int(cmd.Int("limit")))
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			return statusError(writeAppResult(apppublish.Success(result), cmd.Bool("json"), output, diagnostics))
		}},

		{Name: "status-template", Usage: "Read a template by stable identity (candidate API; requires matching MGR)", ArgsUsage: "TEMPLATE_ID", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result"), &cli.Uint64Flag{Name: "job-id", Usage: "Read this original publication job", Local: true}}, Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 {
				return argumentFailure(ctx, cmd, "Provide TEMPLATE_ID")
			}
			client, err := pinnedTemplateClient(ctx, diagnostics)
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			var result any
			if cmd.IsSet("job-id") {
				result, err = client.PublicationStatus(ctx, cmd.Args().First(), cmd.Uint64("job-id"))
			} else {
				result, err = client.TemplateStatus(ctx, cmd.Args().First())
			}
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			return statusError(writeAppResult(apppublish.Success(result), cmd.Bool("json"), output, diagnostics))
		}},
		{Name: "set-template-distribution", Usage: "Set independent share/market switches (candidate API; requires matching MGR)", ArgsUsage: "TEMPLATE_ID", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result"), stringOption("request-id", "Stable request identity; preserve it on an unknown result", ""), stringOption("expected-revision", "Revision from template status; preserve it on replay", ""), boolOption("share-enabled", "Enable or disable sharing; pass =false to disable"), boolOption("market-enabled", "Enable or disable market listing; pass =false to disable")}, Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 || cmd.String("request-id") == "" || cmd.String("expected-revision") == "" || (!cmd.IsSet("share-enabled") && !cmd.IsSet("market-enabled")) {
				return argumentFailure(ctx, cmd, "Provide TEMPLATE_ID and --share-enabled or --market-enabled")
			}
			update := webtemplate.DistributionUpdate{RequestID: cmd.String("request-id"), ExpectedRevision: cmd.String("expected-revision")}
			if cmd.IsSet("share-enabled") {
				v := cmd.Bool("share-enabled")
				update.ShareEnabled = &v
			}
			if cmd.IsSet("market-enabled") {
				v := cmd.Bool("market-enabled")
				update.MarketEnabled = &v
			}
			client, err := pinnedTemplateClient(ctx, diagnostics)
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			result, err := client.SetTemplateDistribution(ctx, cmd.Args().First(), update)
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			return statusError(writeAppResult(apppublish.Success(result), cmd.Bool("json"), output, diagnostics))
		}},
	}
}

func pinnedTemplateClient(ctx context.Context, diagnostics io.Writer) (webtemplate.Client, error) {
	auth, err := newAuthClient(ctx, diagnostics, true)
	if err != nil {
		return webtemplate.Client{}, err
	}
	pinned, err := auth.PinCredential(ctx)
	if err != nil {
		return webtemplate.Client{}, err
	}
	return webtemplate.Client{Auth: pinned}, nil
}

func publishUnifiedFromWeb(ctx context.Context, cmd *cli.Command, output, diagnostics io.Writer) error {
	if cmd.String("request-id") == "" {
		return argumentFailure(ctx, cmd, "Unified publication requires --request-id; preserve it for unknown-result recovery")
	}
	info, err := os.Stat(cmd.String("file"))
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return argumentFailure(ctx, cmd, "Provide a nonempty reviewed install.zip")
	}
	client, err := pinnedTemplateClient(ctx, diagnostics)
	if err != nil {
		return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
	}
	id := cmd.String("template-id")
	var detail webtemplate.UnifiedTemplate
	if id == "" {
		detail, err = client.CreateTemplate(ctx, cmd.Args().First(), cmd.String("request-id"))
		id = detail.TemplateID
	} else {
		detail, err = client.TemplateStatus(ctx, id)
	}
	if err != nil {
		return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
	}
	if detail.SourceWebID != cmd.Args().First() || detail.SourceWebDeleted || detail.Deleted {
		return argumentFailure(ctx, cmd, "Template source is deleted or does not match WEB_ID")
	}
	result, err := client.PublishTemplate(ctx, id, cmd.String("request-id"), cmd.String("file"))
	if err == nil && cmd.Bool("wait") {
		result, err = client.WaitTemplatePublication(ctx, id, result.JobID)
	}
	if err != nil {
		return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
	}
	return statusError(writeAppResult(apppublish.Success(result), cmd.Bool("json"), output, diagnostics))
}
