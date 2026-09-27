package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/tianacloud/cli/internal/authclient"
)

const nonInteractivePageSize = 20

type listOptions struct {
	nonInteractive bool
}

type instanceFetcher func(ctx context.Context, page, pageSize int) (authclient.InstancePage, error)

func executeSQLiteList(ctx context.Context, options listOptions, stdin io.Reader, output, errorOutput io.Writer) int {
	return executeInstanceList(ctx, options, stdin, output, errorOutput, sqliteManagementScope)
}

func executeInstanceList(ctx context.Context, options listOptions, stdin io.Reader, output, errorOutput io.Writer, scope databaseScope) int {
	client, err := newAuthClient(ctx, errorOutput, options.nonInteractive)
	if err != nil {
		fmt.Fprintln(errorOutput, "tiana:", safeDisplay(err.Error()))
		return 1
	}
	interactive := interactiveListRequested(stdin, output, options.nonInteractive)
	fetch := func(fetchContext context.Context, page, pageSize int) (authclient.InstancePage, error) {
		return client.ListInstances(fetchContext, "", page, pageSize)
	}
	operation := func(operationContext context.Context, _ authclient.Credential) error {
		if interactive {
			return runInteractiveListScoped(operationContext, fetch, stdin, output, scope)
		}
		instances, err := fetchAllInstancesScoped(operationContext, fetch, nonInteractivePageSize, scope)
		if err != nil {
			return err
		}
		if scope.engine == "git" && len(instances) == 0 {
			_, err := fmt.Fprintln(output, "No Git repositories found.")
			return err
		} else {
			return writeInstanceTable(output, instances)
		}
	}
	err = client.RunAuthenticated(ctx, operation)
	if errors.Is(err, authclient.ErrAuthenticationRequired) {
		err = client.RunAuthenticated(ctx, operation)
	}
	if err != nil {
		writeCommandError(errorOutput, err)
		return 1
	}
	return 0
}

func fetchAllInstances(ctx context.Context, fetch instanceFetcher, pageSize int) ([]authclient.Instance, error) {
	return fetchAllInstancesScoped(ctx, fetch, pageSize, databaseScope{})
}

func fetchAllInstancesScoped(ctx context.Context, fetch instanceFetcher, pageSize int, scope databaseScope) ([]authclient.Instance, error) {
	instances := make([]authclient.Instance, 0)
	page := 1
	for {
		result, err := fetch(ctx, page, pageSize)
		if err != nil {
			return nil, err
		}
		instances = append(instances, scope.filter(result.Items)...)
		// Pagination termination must use the unfiltered server page: an
		// all-other-engine page must not hide matching instances on later pages.
		if len(result.Items) == 0 || result.TotalPages == 0 || page >= result.TotalPages {
			return instances, nil
		}
		page++
	}
}

// runInteractiveList prints one page at a time on a terminal, prompting only
// while another page exists. It returns without an error when the user quits.
func runInteractiveList(ctx context.Context, fetch instanceFetcher, stdin io.Reader, stdout io.Writer) error {
	return runInteractiveListScoped(ctx, fetch, stdin, stdout, databaseScope{})
}

func runInteractiveListScoped(ctx context.Context, fetch instanceFetcher, stdin io.Reader, stdout io.Writer, scope databaseScope) error {
	reader := bufio.NewReader(stdin)
	pageSize := listPageSize(stdout)
	page := 1
	for {
		result, err := fetch(ctx, page, pageSize)
		if err != nil {
			return err
		}
		if page == 1 && len(result.Items) == 0 && result.Total == 0 {
			if scope.engine == "git" {
				if _, err := fmt.Fprintln(stdout, "No Git repositories found."); err != nil {
					return err
				}
			} else {
				if _, err := fmt.Fprintln(stdout, "No databases found."); err != nil {
					return err
				}
			}
			return nil
		}
		matches := scope.filter(result.Items)
		if scope.engine != "" && len(matches) == 0 {
			if scope.engine == "git" {
				if _, err := fmt.Fprintln(stdout, "No Git repositories on this page."); err != nil {
					return err
				}
			} else {
				if _, err := fmt.Fprintln(stdout, "No SQLite databases on this page."); err != nil {
					return err
				}
			}
		} else {
			if err := writeInstancePage(stdout, matches); err != nil {
				return err
			}
		}
		if result.TotalPages == 0 || page >= result.TotalPages {
			return nil
		}
		if _, err := fmt.Fprint(stdout, "-- More -- (n/space/Enter: next, q: quit) "); err != nil {
			return err
		}
		next, quit, readErr := readPageCommand(reader)
		if _, err := fmt.Fprintln(stdout); err != nil {
			return err
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
		if quit {
			return nil
		}
		if next {
			page++
		}
	}
}

// readPageCommand consumes one full input line. A terminal in canonical mode
// delivers the key together with Enter, so trailing characters are ignored.
func readPageCommand(reader *bufio.Reader) (next bool, quit bool, err error) {
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return false, false, err
	}
	trimmed := strings.TrimRight(line, "\r\n")
	switch {
	case trimmed == "":
		return true, false, nil
	case strings.HasPrefix(trimmed, "n"), strings.HasPrefix(trimmed, "N"), strings.HasPrefix(trimmed, " "):
		return true, false, nil
	case strings.HasPrefix(trimmed, "q"), strings.HasPrefix(trimmed, "Q"):
		return false, true, nil
	default:
		return false, false, nil
	}
}

var instanceTableHeader = []string{"ID", "NAME", "ENGINE", "STATE", "URL"}

func writeInstanceTable(output io.Writer, instances []authclient.Instance) error {
	if len(instances) == 0 {
		_, err := fmt.Fprintln(output, "No databases found.")
		return err
	}
	return writeInstancePage(output, instances)
}

func writeInstancePage(output io.Writer, instances []authclient.Instance) error {
	rows := make([][]string, 0, len(instances))
	for _, instance := range instances {
		rows = append(rows, []string{instance.ID, instance.DisplayName, instance.Engine, instanceDisplayState(instance), connectionURLColumn(instance)})
	}
	widths := tableWidths(append([][]string{instanceTableHeader}, rows...))
	if _, err := fmt.Fprintln(output, formatTableRow(instanceTableHeader, widths)); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := fmt.Fprintln(output, formatTableRow(row, widths)); err != nil {
			return err
		}
	}
	return nil
}

func tableWidths(rows [][]string) []int {
	widths := make([]int, len(instanceTableHeader))
	for _, row := range rows {
		for index, value := range row {
			value = safeDisplay(value)
			if len(value) > widths[index] {
				widths[index] = len(value)
			}
		}
	}
	return widths
}

func formatTableRow(values []string, widths []int) string {
	parts := make([]string, len(values))
	for index, value := range values {
		value = safeDisplay(value)
		if index == len(values)-1 {
			parts[index] = value
			continue
		}
		parts[index] = fmt.Sprintf("%-*s", widths[index], value)
	}
	return strings.TrimRight(strings.Join(parts, "  "), " ")
}
