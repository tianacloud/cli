package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/urfave/cli/v3"
)

func newWebListCommand(input io.Reader, output, diagnostics io.Writer) *cli.Command {
	return &cli.Command{Name: "list", Usage: "List applications", Description: "Fetch all pages by default. Use --page to read only one 20-item page.", Flags: []cli.Flag{
		boolOption("json", "Write a structured JSON result"),
		&cli.IntFlag{Name: "page", Usage: "Read one page (1-10000), without fetching other pages", Local: true},
		stringOption("search", "Filter by name or instance ID before pagination (requires --page)", ""),
	}, Action: func(ctx context.Context, cmd *cli.Command) error {
		if cmd.NArg() != 0 {
			return argumentFailure(ctx, cmd, "web list does not accept positional arguments")
		}
		page := cmd.Int("page")
		search := strings.TrimSpace(cmd.String("search"))
		if cmd.IsSet("page") && (page < 1 || page > 10000) {
			return argumentFailure(ctx, cmd, "Use --page between 1 and 10000")
		}
		if cmd.IsSet("search") && (!cmd.IsSet("page") || !utf8.ValidString(search) || utf8.RuneCountInString(search) > 200) {
			return argumentFailure(ctx, cmd, "Use --search with --page and at most 200 characters")
		}
		client, err := newAuthClient(ctx, diagnostics, !isTerminal(input))
		if err != nil {
			writeCommandError(diagnostics, err)
			return statusError(1)
		}
		r, e := (apppublish.Runner{Client: client}).Bind(ctx)
		if e != nil {
			return statusError(writeAppResult(apppublish.Failure(e), cmd.Bool("json"), output, diagnostics))
		}
		if cmd.IsSet("page") {
			after := ""
			if page > 1 {
				after = strconv.Itoa(page)
			}
			result, e := r.ListSearchPage(ctx, after, search)
			if e != nil {
				return statusError(writeAppResult(apppublish.Failure(e), cmd.Bool("json"), output, diagnostics))
			}
			if cmd.Bool("json") {
				return statusError(writeAppResult(apppublish.Success(result), true, output, diagnostics))
			}
			if err := writeWebTable(output, result.Items); err != nil {
				return statusError(1)
			}
			_, err := fmt.Fprintf(output, "Page %d of %d (%d applications)\n", result.Page, result.TotalPages, result.Total)
			if err != nil {
				return statusError(1)
			}
			return nil
		}
		return statusError(runWebList(ctx, r, cmd.Bool("json"), output, diagnostics))
	}}
}
func runWebList(ctx context.Context, r apppublish.Runner, jsonMode bool, output, diagnostics io.Writer) int {
	after := ""
	lastRequestID := ""
	all := []apppublish.WebProject{}
	fail := func(e *apppublish.Error) int {
		return writeAppResult(apppublish.Failure(e), jsonMode, output, diagnostics)
	}
	for page := 0; page < 10000; page++ {
		result, e := r.ListPage(ctx, after)
		if e != nil {
			return fail(e)
		}
		lastRequestID = result.RequestID
		all = append(all, result.Items...)
		if result.NextCursor == "" {
			if jsonMode {
				return writeAppResult(apppublish.Success(apppublish.WebProjectPage{Items: all}), true, output, diagnostics)
			}
			if err := writeWebTable(output, all); err != nil {
				return 1
			}
			return 0
		}
		after = result.NextCursor
	}
	return fail(&apppublish.Error{RequestID: lastRequestID, Code: "WEB_LIST_LIMIT", Message: "Too many Web pages to list safely", NextAction: "Inspect the service pagination", ExitCode: 1})
}
func writeWebTable(out io.Writer, items []apppublish.WebProject) error {
	if len(items) == 0 {
		_, err := fmt.Fprintln(out, "No applications found.")
		return err
	}
	header := []string{"ID", "NAME", "ENDPOINT", "MESSAGE"}
	rows := [][]string{header}
	for _, w := range items {
		rows = append(rows, []string{w.ID, w.Name, w.Endpoint, w.Description})
	}
	widths := tableWidths(rows)
	for _, row := range rows {
		if _, err := fmt.Fprintln(out, formatTableRow(row, widths)); err != nil {
			return err
		}
	}
	return nil
}
