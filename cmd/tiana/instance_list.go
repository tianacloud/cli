package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/tianacloud/cli/internal/apppublish"
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
	client, err := newAuthClient(ctx, errorOutput, options.nonInteractive || managementJSON(ctx))
	if err != nil {
		if managementJSON(ctx) {
			return managementFailure(err, output, errorOutput, nil)
		}
		fmt.Fprintln(errorOutput, "tiana:", safeDisplay(err.Error()))
		return 1
	}
	fetch := func(fetchContext context.Context, page, pageSize int) (authclient.InstancePage, error) {
		return client.ListInstances(fetchContext, "", page, pageSize)
	}
	operation := func(operationContext context.Context, _ authclient.Credential) error {
		instances, err := fetchAllInstancesScoped(operationContext, fetch, nonInteractivePageSize, scope)
		if err != nil {
			return err
		}
		if managementJSON(ctx) {
			if code := writeAppResult(apppublish.Success(instanceListJSON(instances)), true, output, errorOutput); code != 0 {
				return errors.New("cannot write instance list")
			}
			return nil
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
		if managementJSON(ctx) {
			return managementFailure(err, output, errorOutput, nil)
		}
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

var instanceTableHeader = []string{"ID", "NAME", "STATE", "URL", "MESSAGE"}

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
		rows = append(rows, []string{instance.ID, instance.DisplayName, instanceDisplayState(instance), connectionURLColumn(instance), instance.Notes})
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
