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
	page, err := client.ListBranches(ctx, instance.ID, after, search, "")
	if err != nil {
		writeCommandError(diagnostics, err)
		return 1
	}
	table := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "NAME\tBRANCH ID\tDEFAULT\tSTATE\tRUNTIME\tENDPOINT")
	for _, branch := range page.Items {
		fmt.Fprintf(table, "%s\t%s\t%t\t%s\t%s\t%s\n", safeDisplay(branch.Name), safeDisplay(branch.ID), branch.Root, safeDisplay(branch.LifecycleState), safeDisplay(branch.RuntimeState), safeDisplay(branch.EndpointID))
	}
	_ = table.Flush()
	if page.NextCursor != "" {
		fmt.Fprintf(output, "Next cursor: %s\n", safeDisplay(page.NextCursor))
	}
	return 0
}
