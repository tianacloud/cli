package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

func runStatus(ctx context.Context, output, diagnostics io.Writer) int {
	client, err := newAuthClient(ctx, diagnostics, true)
	if err != nil {
		writeCommandError(diagnostics, err)
		return 1
	}
	user, err := client.Whoami(ctx)
	if err != nil {
		if errors.Is(err, authclient.ErrAuthenticationRequired) {
			fmt.Fprintln(output, "Not signed in\nQuota: unavailable (sign in first)")
		}
		writeCommandError(diagnostics, err)
		return 1
	}
	if user.ID == "" {
		fmt.Fprintln(diagnostics, "tiana: invalid account status response")
		return 1
	}
	for _, field := range []struct{ label, value string }{{"Email", user.Email}, {"Name", user.DisplayName}, {"Username", user.Username}} {
		if field.value != "" {
			if _, err := fmt.Fprintf(output, "%s: %s\n", field.label, safeDisplay(field.value)); err != nil {
				fmt.Fprintln(diagnostics, "tiana: cannot write status")
				return 1
			}
		}
	}
	quota, err := client.GetTenantUsage(ctx)
	if err != nil {
		fmt.Fprintln(output, "Quota: unavailable")
		writeCommandError(diagnostics, err)
		return 1
	}
	table := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	fmt.Fprintf(table, "Quota period: %s (%s to %s)\n", safeDisplay(quota.Limits.Period), statusTime(quota.PeriodStart), statusTime(quota.PeriodEnd))
	fmt.Fprintln(table, "RESOURCE\tUSED\tLIMIT")
	fmt.Fprintf(table, "Compute\t%s\t%d\nStorage (bytes)\t%s\t%d\nInstances\t%s\t%d\n", statusQuantity(quota.ComputeUsed), *quota.Limits.Compute, statusQuantity(quota.StorageUsed), *quota.Limits.StorageBytes, statusQuantity(quota.InstancesUsed), *quota.Limits.MaxInstances)
	blocked := "no"
	if *quota.Blocked {
		blocked = "yes"
	}
	fmt.Fprintf(table, "Blocked: %s\n", blocked)
	if quota.Reason != "" {
		fmt.Fprintf(table, "Reason: %s\n", safeDisplay(quota.Reason))
	}
	fmt.Fprintf(table, "Usage updated at: %s\n", statusTime(quota.UpdatedAt))
	if err := table.Flush(); err != nil {
		fmt.Fprintln(diagnostics, "tiana: cannot write status")
		return 1
	}
	return 0
}

func statusQuantity(value *uint64) string {
	if value == nil {
		return "unknown"
	}
	return strconv.FormatUint(*value, 10)
}
func statusTime(value *int64) string {
	if value == nil {
		return "unknown"
	}
	return time.UnixMilli(*value).UTC().Format(time.RFC3339Nano)
}
