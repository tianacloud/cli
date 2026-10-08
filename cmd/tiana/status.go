package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
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
	fmt.Fprintln(table, "RESOURCE\tUSED\tLIMIT\tUSAGE\tPROGRESS")
	for _, resource := range []struct {
		name  string
		used  *uint64
		limit uint64
	}{
		{"Compute", quota.ComputeUsed, *quota.Limits.Compute},
		{"Storage (bytes)", quota.StorageUsed, *quota.Limits.StorageBytes},
		{"Instances", quota.InstancesUsed, *quota.Limits.MaxInstances},
	} {
		percent, bar := statusQuotaProgress(resource.used, resource.limit)
		fmt.Fprintf(table, "%s\t%s\t%d\t%s\t%s\n", resource.name, statusQuantity(resource.used), resource.limit, percent, bar)
	}
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

// Keep uint64 quantities exact, including when multiplying them to form a
// percentage. A zero limit is a real quota, not an unlimited sentinel.
func statusQuotaProgress(used *uint64, limit uint64) (string, string) {
	if used == nil {
		return "unknown", "-"
	}
	if limit == 0 {
		return "n/a", "-"
	}
	numerator := new(big.Int).SetUint64(*used)
	denominator := new(big.Int).SetUint64(limit)
	ratio := new(big.Rat).SetFrac(numerator, denominator)
	percent := ratio.Mul(ratio, big.NewRat(100, 1)).FloatString(1)
	// Do not let display rounding hide nonzero use or which side of the limit
	// usage lies on. Bar cells use the exact fraction, not the rounded percent.
	switch {
	case *used > 0 && percent == "0.0":
		percent = "<0.1"
	case *used < limit && percent == "100.0":
		percent = ">99.9"
	case *used > limit && percent == "100.0":
		percent = ">100.0"
	}
	const width = 20
	filled := width
	if *used < limit {
		cells := new(big.Int).Mul(numerator, big.NewInt(width))
		filled = int(cells.Quo(cells, denominator).Int64())
	}
	return percent + "%", "[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]"
}
