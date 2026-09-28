package main

import (
	"bufio"
	"context"
	"fmt"
	"io"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/urfave/cli/v3"
)

func newAppListCommand(input io.Reader, output, diagnostics io.Writer) *cli.Command {
	return &cli.Command{Name: "list", Usage: "List applications", Description: "An interactive terminal pages the results; otherwise all pages are printed once.", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result")}, Action: func(ctx context.Context, cmd *cli.Command) error {
		if cmd.NArg() != 0 {
			return argumentFailure(ctx, cmd, "app list does not accept positional arguments")
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
		return statusError(runAppList(ctx, r, cmd.Bool("json"), interactiveListRequested(input, output, false), input, output, diagnostics))
	}}
}
func runAppList(ctx context.Context, r apppublish.Runner, jsonMode, interactive bool, input io.Reader, output, diagnostics io.Writer) int {
	after := ""
	lastRequestID := ""
	all := []apppublish.App{}
	reader := bufio.NewReader(input)
	fail := func(e *apppublish.Error) int {
		return writeAppResult(apppublish.Failure(e), jsonMode, output, diagnostics)
	}
	for page := 0; page < 10000; page++ {
		result, e := r.ListPage(ctx, after)
		if e != nil {
			return fail(e)
		}
		lastRequestID = result.RequestID
		if interactive && !jsonMode {
			if err := writeAppTable(output, result.Items); err != nil {
				return 1
			}
		} else {
			all = append(all, result.Items...)
		}
		if result.NextCursor == "" {
			if jsonMode {
				return writeAppResult(apppublish.Success(apppublish.AppPage{Items: all}), true, output, diagnostics)
			}
			if !interactive {
				if err := writeAppTable(output, all); err != nil {
					return 1
				}
			}
			return 0
		}
		if interactive && !jsonMode {
			for {
				if _, err := fmt.Fprint(output, "-- More -- (n/space/Enter: next, q: quit) "); err != nil {
					return 1
				}
				next, quit, err := readPageCommand(reader)
				if _, err := fmt.Fprintln(output); err != nil {
					return 1
				}
				if quit || err == io.EOF {
					return 0
				}
				if err != nil {
					return 1
				}
				if next {
					break
				}
			}
		}
		after = result.NextCursor
	}
	return fail(&apppublish.Error{RequestID: lastRequestID, Code: "APP_LIST_LIMIT", Message: "Too many App pages to list safely", NextAction: "Inspect the service pagination", ExitCode: 1})
}
func writeAppTable(out io.Writer, items []apppublish.App) error {
	if len(items) == 0 {
		_, err := fmt.Fprintln(out, "No applications found.")
		return err
	}
	header := []string{"ID", "NAME"}
	rows := [][]string{header}
	for _, w := range items {
		rows = append(rows, []string{w.AppID, w.Name})
	}
	widths := tableWidths(rows)
	for _, row := range rows {
		if _, err := fmt.Fprintln(out, formatTableRow(row, widths)); err != nil {
			return err
		}
	}
	return nil
}
