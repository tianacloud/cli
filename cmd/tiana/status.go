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
	credential, err := client.EnsureCredential(ctx)
	var account authclient.AccountState
	if err == nil {
		account, err = client.AccountState(ctx)
	}
	if err == nil {
		// Account verification retains the SDK's normal same-space refresh.
		// Pin the resulting credential before reading space metadata and quota.
		var current authclient.Credential
		current, err = client.EnsureCredential(ctx)
		if err == nil {
			err = bindOperationIdentity(&credential, current)
			client = client.WithCredential(current)
		}
	}
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
	user := account.User
	if user.ID == "" || user.ID != credential.User.ID || user.TenantID != credential.User.TenantID {
		return spaceFailure(ctx, errors.New("account response does not match the selected space"), false, nil, "Run tiana status again", output, diagnostics)
	}
	space, spaceErr := client.CurrentSpace(ctx)
	quota, quotaErr := client.GetTenantUsage(ctx)
	data := map[string]any{"logged_in": true, "user": user, "space": nil, "quota": nil, "sync_status": account.SyncStatus}
	if account.JobID != 0 {
		data["job_id"] = strconv.FormatUint(account.JobID, 10)
	}
	if spaceErr == nil {
		data["space"] = space
	}
	if quotaErr == nil {
		data["quota"] = statusQuotaData(quota)
	}
	if spaceErr != nil {
		writeCommandError(diagnostics, spaceErr)
	}
	if quotaErr != nil {
		writeCommandError(diagnostics, quotaErr)
	}
	if managementJSON(ctx) {
		if spaceErr != nil || quotaErr != nil {
			code, message := "SPACE_UNAVAILABLE", "Account verified but space information is unavailable"
			if quotaErr != nil {
				code, message = "QUOTA_UNAVAILABLE", "Account verified but quota is unavailable"
			}
			return writeAppResult(apppublish.Result{Status: "failed", Data: data, Error: &apppublish.Error{Code: code, Message: message, NextAction: "Query tiana status again when the service is available", ExitCode: 1}}, true, output, diagnostics)
		}
		return writeAppResult(apppublish.Success(data), true, output, diagnostics)
	}
	table := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	for _, field := range []struct{ label, value string }{{"Email", user.Email}, {"Name", user.DisplayName}, {"Username", user.Username}} {
		if field.value != "" {
			fmt.Fprintf(table, "%s: %s\n", field.label, safeDisplay(field.value))
		}
	}
	if spaceErr == nil {
		fmt.Fprintf(table, "Space: %s\nRole: %s\n", safeDisplay(spaceName(space.Space)), safeDisplay(space.Role))
	} else {
		fmt.Fprintln(table, "Space: unavailable")
	}
	syncStatus := account.SyncStatus
	if syncStatus == "" {
		syncStatus = "unknown"
	}
	fmt.Fprintf(table, "Data credential sync: %s\n", safeDisplay(syncStatus))
	if quotaErr != nil {
		fmt.Fprintln(table, "Quota: unavailable")
	} else {
		writeStatusQuota(table, quota)
	}
	if err := table.Flush(); err != nil {
		fmt.Fprintln(diagnostics, "tiana: cannot write status")
		return 1
	}
	if spaceErr != nil || quotaErr != nil {
		return 1
	}
	return 0
}

func statusQuotaData(quota authclient.TenantUsage) map[string]any {
	return map[string]any{
		"tenant_id": quota.TenantID, "compute_used": quantityJSON(quota.ComputeUsed), "storage_used": quantityJSON(quota.StorageUsed), "instances_used": quantityJSON(quota.InstancesUsed),
		"public_network_used": quantityJSON(quota.PublicNetworkUsed), "file_compute_used": quantityJSON(quota.FileComputeUsed), "file_storage_used": quantityJSON(quota.FileStorageUsed), "file_reserved_bytes": quantityJSON(quota.FileReservedBytes),
		"updated_at": millisJSON(quota.UpdatedAt), "period_start": millisJSON(quota.PeriodStart), "period_end": millisJSON(quota.PeriodEnd), "limits": quota.Limits, "blocked": quota.Blocked, "reason": quota.Reason, "public_network_blocked": quota.PublicNetworkBlocked,
	}
}

func writeStatusQuota(table io.Writer, quota authclient.TenantUsage) {
	fmt.Fprintf(table, "Quota period: %s (%s to %s)\n", safeDisplay(quota.Limits.Period), statusTime(quota.PeriodStart), statusTime(quota.PeriodEnd))
	fmt.Fprintln(table, "RESOURCE\tUSED\tLIMIT\tUSAGE\tPROGRESS")
	for _, resource := range []struct {
		name        string
		used, limit *uint64
	}{
		{"Compute", quota.ComputeUsed, quota.Limits.Compute},
		{"Storage (bytes)", quota.StorageUsed, quota.Limits.StorageBytes},
		{"Instances", quota.InstancesUsed, quota.Limits.MaxInstances},
		{"Public network (bytes)", quota.PublicNetworkUsed, quota.Limits.PublicNetworkBytes},
	} {
		percent, bar := "unknown", "-"
		if resource.limit != nil {
			percent, bar = statusQuotaProgress(resource.used, *resource.limit)
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", resource.name, statusQuantity(resource.used), statusQuantity(resource.limit), percent, bar)
	}
	fmt.Fprintf(table, "Blocked: %s\n", statusBool(quota.Blocked))
	if quota.Reason != "" {
		fmt.Fprintf(table, "Reason: %s\n", safeDisplay(quota.Reason))
	}
	fmt.Fprintf(table, "Public network blocked: %s\n", statusBool(quota.PublicNetworkBlocked))
	fmt.Fprintf(table, "Usage updated at: %s\n", statusTime(quota.UpdatedAt))
}

func statusBool(value *bool) string {
	if value == nil {
		return "unknown"
	}
	if *value {
		return "yes"
	}
	return "no"
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
