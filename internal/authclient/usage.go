package authclient

import (
	"context"
	"errors"
	"net/http"
)

// TenantUsage consumes only the authenticated tenant's summary. Quantities are
// decimal JSON strings on the wire; timestamps are Unix milliseconds. Pointers
// distinguish unknown/missing data from real zero values.
type TenantUsage struct {
	TenantID          string  `json:"tenant_id"`
	ComputeUsed       *uint64 `json:"compute_used,string"`
	StorageUsed       *uint64 `json:"storage_used,string"`
	InstancesUsed     *uint64 `json:"instances_used,string"`
	PublicNetworkUsed *uint64 `json:"public_network_used,string"`
	FileComputeUsed   *uint64 `json:"file_compute_used,string"`
	FileStorageUsed   *uint64 `json:"file_storage_used,string"`
	FileReservedBytes *uint64 `json:"file_reserved_bytes,string"`
	UpdatedAt         *int64  `json:"updated_at"`
	PeriodStart       *int64  `json:"period_start"`
	PeriodEnd         *int64  `json:"period_end"`
	Limits            struct {
		Compute            *uint64 `json:"compute,string"`
		StorageBytes       *uint64 `json:"storage_bytes,string"`
		MaxInstances       *uint64 `json:"max_instances,string"`
		PublicNetworkBytes *uint64 `json:"public_network_bytes,string"`
		Period             string  `json:"period"`
	} `json:"limits"`
	Blocked              *bool  `json:"blocked"`
	Reason               string `json:"reason"`
	PublicNetworkBlocked *bool  `json:"public_network_blocked"`
}

func (c *Client) GetTenantUsage(ctx context.Context) (TenantUsage, error) {
	var result TenantUsage
	// Tenant totals are independent of detail pagination. Bound unused detail
	// to one instance/branch and never supply a caller-selected tenant ID.
	if _, err := c.DoJSON(ctx, http.MethodGet, "/api/v1/usage?page=1&page_size=1", nil, nil, &result); err != nil {
		return TenantUsage{}, err
	}
	if result.TenantID == "" || result.ComputeUsed == nil || result.InstancesUsed == nil || result.Limits.Compute == nil || result.Limits.StorageBytes == nil || result.Limits.MaxInstances == nil || result.Limits.Period == "" || result.Blocked == nil || result.PeriodStart == nil || result.PeriodEnd == nil || *result.PeriodStart < 0 || *result.PeriodEnd <= *result.PeriodStart || (result.UpdatedAt != nil && *result.UpdatedAt < 0) {
		return TenantUsage{}, errors.New("incomplete or invalid quota response")
	}
	if c.credential != nil && result.TenantID != c.credential.User.TenantID {
		return TenantUsage{}, errors.New("quota response does not match the selected space")
	}
	return result, nil
}
