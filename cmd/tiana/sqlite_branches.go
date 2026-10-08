package main

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tianacloud/cli/internal/authclient"
)

func resolveSQLiteBranchWithLogin(ctx context.Context, client *authclient.Client, reference, name string) (authclient.Instance, authclient.BranchDetail, error) {
	instance, err := resolveInstanceWithLogin(ctx, client, reference)
	if err != nil {
		return instance, authclient.BranchDetail{}, err
	}
	if err = sqliteManagementScope.check(instance); err != nil {
		return instance, authclient.BranchDetail{}, err
	}
	detail, err := client.ResolveBranch(ctx, instance.ID, name)
	if err == nil {
		instance.EndpointID = detail.Branch.EndpointID
		instance.Connection = detail.Connection
	}
	return instance, detail, err
}

func executeSQLiteBranchesList(ctx context.Context, reference, after, search string, output, diagnostics io.Writer) int {
	client, err := newAuthClient(ctx, diagnostics, false)
	if err != nil {
		writeCommandError(diagnostics, err)
		return 1
	}
	instance, err := resolveInstanceWithLogin(ctx, client, reference)
	if err != nil {
		reportResolveError(diagnostics, reference, err)
		return 1
	}
	if err = sqliteManagementScope.check(instance); err != nil {
		writeCommandError(diagnostics, err)
		return 1
	}
	branches, err := fetchAllBranches(ctx, client, instance.ID, after, search)
	if err != nil {
		writeCommandError(diagnostics, err)
		return 1
	}
	table := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "NAME\tBRANCH ID\tDEFAULT\tSTATE\tRUNTIME\tENDPOINT\tMESSAGE")
	for _, branch := range branches {
		fmt.Fprintf(table, "%s\t%s\t%t\t%s\t%s\t%s\t%s\n", safeDisplay(branch.Name), safeDisplay(branch.ID), branch.Root, safeDisplay(branch.LifecycleState), safeDisplay(branch.RuntimeState), safeDisplay(branch.EndpointID), safeDisplay(branch.Notes))
	}
	if err := table.Flush(); err != nil {
		writeCommandError(diagnostics, err)
		return 1
	}
	return 0
}

func fetchAllBranches(ctx context.Context, client *authclient.Client, instanceID, after, search string) ([]authclient.Branch, error) {
	var branches []authclient.Branch
	seen := map[string]bool{after: true}
	for page := 0; page < 10000; page++ {
		result, err := client.ListBranches(ctx, instanceID, after, search, "")
		if err != nil {
			return nil, err
		}
		branches = append(branches, result.Items...)
		if result.NextCursor == "" {
			return branches, nil
		}
		if seen[result.NextCursor] {
			return nil, fmt.Errorf("invalid SQLite branch pagination: cursor did not advance")
		}
		seen[result.NextCursor] = true
		after = result.NextCursor
	}
	return nil, fmt.Errorf("too many SQLite branch pages to list safely")
}
