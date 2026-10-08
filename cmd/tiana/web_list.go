package main

import (
	"context"
	"fmt"
	"io"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/urfave/cli/v3"
)

func newWebListCommand(input io.Reader, output, diagnostics io.Writer) *cli.Command {
	return &cli.Command{Name: "list", Usage: "List applications", Description: "Fetch all pages and display every application without prompting.", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result")}, Action: func(ctx context.Context, cmd *cli.Command) error {
		if cmd.NArg() != 0 {
			return argumentFailure(ctx, cmd, "web list does not accept positional arguments")
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
