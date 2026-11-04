package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/functions"
	"github.com/urfave/cli/v3"
)

func newFunctionRuntimeLogsCommand(output, diagnostics io.Writer) *cli.Command {
	return &cli.Command{Name: "runtime-logs", Usage: "Read one page of runtime stdout/stderr without waking the function", ArgsUsage: "INSTANCE_ID", Flags: append([]cli.Flag{
		&cli.DurationFlag{Name: "since", Value: 30 * time.Minute, Usage: "Look back from now (for example 30m or 2h)"},
		&cli.StringFlag{Name: "start", Usage: "Inclusive range start (RFC3339, fractional seconds supported)"},
		&cli.StringFlag{Name: "end", Usage: "Range end (RFC3339, defaults to now)"},
		&cli.StringFlag{Name: "stream", Usage: "stdout or stderr; omit for both"},
		&cli.StringFlag{Name: "cursor", Usage: "Read the next page using next_cursor from the previous response"},
	}, functionJSONFlag()), Action: func(ctx context.Context, cmd *cli.Command) error {
		if cmd.NArg() != 1 || strings.TrimSpace(cmd.Args().First()) == "" {
			return argumentFailure(ctx, cmd, "one function ID is required")
		}
		return runFunctionCommand(ctx, cmd, output, diagnostics, func(c *functions.Client) (apppublish.Result, error) {
			query := url.Values{}
			if cursor := cmd.String("cursor"); cursor != "" {
				for _, flag := range []string{"since", "start", "end", "stream"} {
					if cmd.IsSet(flag) {
						return apppublish.Result{}, errors.New("cursor already selects the time range and output stream")
					}
				}
				query.Set("cursor", cursor)
			} else {
				if cmd.IsSet("since") && (cmd.IsSet("start") || cmd.IsSet("end")) {
					return apppublish.Result{}, errors.New("use since or start/end, not both")
				}
				end := time.Now().UTC()
				if value := cmd.String("end"); value != "" {
					var err error
					end, err = time.Parse(time.RFC3339Nano, value)
					if err != nil {
						return apppublish.Result{}, errors.New("end must be RFC3339")
					}
				}
				since := cmd.Duration("since")
				if since <= 0 {
					return apppublish.Result{}, errors.New("since must be positive")
				}
				start := end.Add(-since)
				if value := cmd.String("start"); value != "" {
					var err error
					start, err = time.Parse(time.RFC3339Nano, value)
					if err != nil {
						return apppublish.Result{}, errors.New("start must be RFC3339")
					}
				}
				if start.After(end) {
					return apppublish.Result{}, errors.New("start must not be after end")
				}
				query.Set("start", start.Format(time.RFC3339Nano))
				query.Set("end", end.Format(time.RFC3339Nano))
				if stream := cmd.String("stream"); stream != "" {
					if stream != "stdout" && stream != "stderr" {
						return apppublish.Result{}, errors.New("stream must be stdout or stderr")
					}
					query.Set("stream", stream)
				}
			}
			var page authclient.FunctionRuntimeLogPage
			err := c.Request(ctx, "GET", functions.Path(cmd.Args().First())+"/runtime-logs?"+query.Encode(), nil, &page)
			return apppublish.Success(page), err
		})
	}}
}

func writeFunctionRuntimeLogs(output io.Writer, page authclient.FunctionRuntimeLogPage) error {
	if _, err := fmt.Fprintf(output, "Collection time: %s — %s · retention %dh\n", safeDisplay(page.Start), safeDisplay(page.End), page.RetentionHours); err != nil {
		return err
	}
	if len(page.Items) == 0 {
		_, err := fmt.Fprintln(output, "No runtime logs in this time range.")
		return err
	}
	for _, row := range page.Items {
		// Keep log line breaks, while rendering terminal controls visibly. JSON
		// output preserves the collected message without presentation changes.
		lines := strings.Split(row.Message, "\n")
		for i := range lines {
			lines[i] = safeDisplay(lines[i])
		}
		if _, err := fmt.Fprintf(output, "%s [%s] %s\n", safeDisplay(row.Timestamp), safeDisplay(row.Stream), strings.Join(lines, "\n")); err != nil {
			return err
		}
	}
	if page.NextCursor != "" {
		_, err := fmt.Fprintf(output, "Next page: --cursor %s\n", safeDisplay(page.NextCursor))
		return err
	}
	return nil
}
