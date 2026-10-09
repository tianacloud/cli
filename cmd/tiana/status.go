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

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
)

func runStatus(ctx context.Context, output, diagnostics io.Writer) int {
	client, err := newAuthClient(ctx, diagnostics, true)
	if err != nil {
		if managementJSON(ctx) {
			return managementFailure(err, output, diagnostics, nil)
		}
		writeCommandError(diagnostics, err)
		return 1
	}
	user, err := client.Whoami(ctx)
	if err != nil {
		if managementJSON(ctx) {
			return managementFailure(err, output, diagnostics, nil)
		}
		if errors.Is(err, authclient.ErrAuthenticationRequired) {
			fmt.Fprintln(output, "Not signed in\nQuota: unavailable (sign in first)")
		}
		writeCommandError(diagnostics, err)
		return 1
	}
	if user.ID == "" {
		if managementJSON(ctx) {
			return writeAppResult(apppublish.Failure(&apppublish.Error{Code: "INVALID_ACCOUNT_RESPONSE", Message: "Invalid account status response", ExitCode: 1}), true, output, diagnostics)
		}
		fmt.Fprintln(diagnostics, "tiana: invalid account status response")
		return 1
	}
	if !managementJSON(ctx) {
		for _, field := range []struct{ label, value string }{{"Email", user.Email}, {"Name", user.DisplayName}, {"Username", user.Username}} {
			if field.value != "" {
				if _, err := fmt.Fprintf(output, "%s: %s\n", field.label, safeDisplay(field.value)); err != nil {
					fmt.Fprintln(diagnostics, "tiana: cannot write status")
					return 1
				}
			}
		}
	}
	quota, err := client.GetTenantUsage(ctx)
	if err != nil {
		if managementJSON(ctx) {
			writeCommandError(diagnostics, err)
			return writeAppResult(apppublish.Result{Status: "failed", Data: map[string]any{"logged_in": true, "user": user, "quota": nil}, Error: &apppublish.Error{Code: "QUOTA_UNAVAILABLE", Message: "Account verified but quota is unavailable", NextAction: "Query tiana status again when quota service is available", ExitCode: 1}}, true, output, diagnostics)
		}
		fmt.Fprintln(output, "Quota: unavailable")
		writeCommandError(diagnostics, err)
		return 1
	}
	if managementJSON(ctx) {
		data := map[string]any{"logged_in": true, "user": user, "quota": map[string]any{"tenant_id": quota.TenantID, "compute_used": quota.ComputeUsed, "storage_used": quota.StorageUsed, "instances_used": quota.InstancesUsed, "updated_at": millisJSON(quota.UpdatedAt), "period_start": millisJSON(quota.PeriodStart), "period_end": millisJSON(quota.PeriodEnd), "limits": quota.Limits, "blocked": quota.Blocked, "reason": quota.Reason}}
		// Encode uint64 quantities as decimal strings independently of API decoding.
		q := data["quota"].(map[string]any)
		q["compute_used"] = quantityJSON(quota.ComputeUsed)
		q["storage_used"] = quantityJSON(quota.StorageUsed)
		q["instances_used"] = quantityJSON(quota.InstancesUsed)
		return writeAppResult(apppublish.Success(data), true, output, diagnostics)
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

func millisJSON(value *int64) any {
	if value == nil {
		return nil
	}
	return strconv.FormatInt(*value, 10)
}
func quantityJSON(value *uint64) any {
	if value == nil {
		return nil
	}
	return strconv.FormatUint(*value, 10)
}
