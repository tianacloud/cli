package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/webtemplate"
	"github.com/urfave/cli/v3"
)

func newUnifiedTemplateCommands(output, diagnostics io.Writer) []*cli.Command {
	return []*cli.Command{
		{Name: "init-unified-template", Usage: "Download source by template ID (candidate API; requires matching MGR)", ArgsUsage: "TEMPLATE_ID", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result"), stringOption("channel", "share or market", "market"), stringOption("version", "Fixed template version, if selected", ""), stringOption("dir", "New project directory", "")}, Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 || cmd.String("dir") == "" || !webtemplate.ValidTemplateID(cmd.Args().First()) || (cmd.String("channel") != "share" && cmd.String("channel") != "market") || (cmd.String("version") != "" && (!webtemplate.ValidDecimal(cmd.String("version")) || cmd.String("version") == "0")) {
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
			if cmd.NArg() != 1 || !webtemplate.ValidTemplateID(cmd.Args().First()) || (cmd.IsSet("job-id") && cmd.Uint64("job-id") == 0) {
				return argumentFailure(ctx, cmd, "Provide TEMPLATE_ID and a nonzero job identity")
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
			if cmd.NArg() != 1 || strings.TrimSpace(cmd.String("request-id")) == "" || !webtemplate.ValidTemplateID(cmd.Args().First()) || !webtemplate.ValidDecimal(cmd.String("expected-revision")) || (!cmd.IsSet("share-enabled") && !cmd.IsSet("market-enabled")) {
				return argumentFailure(ctx, cmd, "Provide TEMPLATE_ID and --share-enabled or --market-enabled")
			}
			release, err := lockManagementMutation(ctx, diagnostics)
			if err != nil {
				return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
			}
			defer release()
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
				data := map[string]any{"template_id": cmd.Args().First(), "request_id": update.RequestID, "expected_revision": update.ExpectedRevision}
				if update.ShareEnabled != nil {
					data["share_enabled"] = *update.ShareEnabled
				}
				if update.MarketEnabled != nil {
					data["market_enabled"] = *update.MarketEnabled
				}
				return templateMutationFailure(ctx, err, data, false, true, cmd.Bool("json"), output, diagnostics)
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
	if strings.TrimSpace(cmd.String("request-id")) == "" || strings.TrimSpace(cmd.Args().First()) == "" || (cmd.String("template-id") != "" && !webtemplate.ValidTemplateID(cmd.String("template-id"))) {
		return argumentFailure(ctx, cmd, "Unified publication requires --request-id; preserve it for unknown-result recovery")
	}
	info, err := os.Stat(cmd.String("file"))
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return argumentFailure(ctx, cmd, "Provide a nonempty reviewed install.zip")
	}
	release, err := lockManagementMutation(ctx, diagnostics)
	if err != nil {
		return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
	}
	defer release()
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
		if cmd.String("template-id") != "" {
			return templateCommandFailure(err, cmd.Bool("json"), output, diagnostics)
		}
		return templateMutationFailure(ctx, err, map[string]any{"source_web_id": cmd.Args().First(), "request_id": cmd.String("request-id")}, false, true, cmd.Bool("json"), output, diagnostics)
	}
	if detail.SourceWebID != cmd.Args().First() || detail.SourceWebDeleted || detail.Deleted {
		return argumentFailure(ctx, cmd, "Template source is deleted or does not match WEB_ID")
	}
	result, err := client.PublishTemplate(ctx, id, cmd.String("request-id"), cmd.String("file"))
	if err == nil && cmd.Bool("wait") {
		accepted := result
		result, err = client.WaitTemplatePublication(ctx, id, result.JobID)
		if result.JobID != accepted.JobID || result.TemplateID != id {
			result = accepted
		}
	}
	if err != nil {
		if result.TemplateID != id {
			result = webtemplate.TemplatePublication{}
		}
		data := map[string]any{"source_web_id": cmd.Args().First(), "template_id": id, "request_id": cmd.String("request-id")}
		if result.JobID != 0 {
			data["job_id"] = result.JobID
			data["publication_status"] = result.Status
		}
		return templateMutationFailure(ctx, err, data, errors.Is(err, webtemplate.ErrPublicationFailed), false, cmd.Bool("json"), output, diagnostics)
	}
	if result.Status != "complete" {
		return statusError(writeAppResult(apppublish.Result{Status: "accepted", Data: result}, cmd.Bool("json"), output, diagnostics))
	}
	return statusError(writeAppResult(apppublish.Success(result), cmd.Bool("json"), output, diagnostics))
}

// Retain immutable recovery inputs without copying untrusted server errors into output.
func templateMutationFailure(ctx context.Context, err error, data map[string]any, definite, allowRejection bool, jsonMode bool, output, diagnostics io.Writer) error {
	var api *authclient.APIError
	if allowRejection && errors.As(err, &api) && api.Status >= 400 && api.Status < 500 && api.Status != 408 && api.Code != "COMMIT_STATUS_UNKNOWN" && api.Code != "RESULT_UNKNOWN" {
		definite = true
	}
	code, status, errorCode := 4, "unknown", "TEMPLATE_OUTCOME_UNKNOWN"
	if definite {
		code, status, errorCode = 1, "failed", "WEB_TEMPLATE_FAILED"
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		code, status, errorCode = 130, "unknown", "OBSERVATION_STOPPED"
	}
	result := apppublish.Result{Status: status, Data: data, Error: &apppublish.Error{Code: errorCode, Message: "Template operation did not complete", NextAction: "Preserve the original request, target and any package; inspect the original request or job before retrying", ExitCode: code}}
	if !jsonMode {
		for _, key := range []string{"source_web_id", "template_id", "request_id", "job_id", "expected_revision", "share_enabled", "market_enabled"} {
			if value, ok := data[key]; ok {
				fmt.Fprintf(diagnostics, "%s: %s\n", key, safeDisplay(fmt.Sprint(value)))
			}
		}
	}
	return statusError(writeAppResult(result, jsonMode, output, diagnostics))
}
