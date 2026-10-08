package authclient

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// BranchOperationReceipt reports durable acceptance, not operation completion.
type BranchOperationReceipt struct {
	InstanceID  string `json:"instance_id"`
	OperationID string `json:"operation_id"`
}

type CreateBranchRequest struct {
	Name       string  `json:"name"`
	Notes      string  `json:"notes,omitempty"`
	Timestamp  *uint64 `json:"timestamp,omitempty"`
	TTLSeconds *int64  `json:"ttl_seconds"`
}

func ValidResourcePathID(id string) bool {
	return strings.TrimSpace(id) != "" && id != "." && id != ".." && !strings.ContainsAny(id, "/\\\r\n\t ")
}

// ResolveMutationBranch pins and validates the immutable target. Name resolution
// is exact; explicit IDs never fall back to names after a failed lookup.
func (c *Client) ResolveMutationBranch(ctx context.Context, instanceID, ref string, byID bool) (BranchDetail, error) {
	id := ref
	if !byID {
		page, err := c.ListBranches(ctx, instanceID, "", "", ref)
		if err != nil {
			return BranchDetail{}, err
		}
		if len(page.Items) != 1 || page.Items[0].Name != ref {
			return BranchDetail{}, errors.New("branch name is missing or ambiguous")
		}
		id = page.Items[0].ID
	}
	if !ValidResourcePathID(instanceID) || !ValidResourcePathID(id) {
		return BranchDetail{}, errors.New("invalid branch target identity")
	}
	detail, err := c.GetBranch(ctx, instanceID, id)
	if err != nil {
		return BranchDetail{}, err
	}
	if detail.InstanceID != instanceID || detail.Branch.ID != id || (!byID && detail.Branch.Name != ref) {
		return BranchDetail{}, errors.New("branch identity changed or does not match the requested target")
	}
	return detail, nil
}

func (c *Client) CreateBranch(ctx context.Context, instanceID, parentID string, request CreateBranchRequest, key string) (BranchOperationReceipt, error) {
	return c.branchMutation(ctx, http.MethodPost, instanceID, parentID, "/children", request, key)
}
func (c *Client) DeleteBranch(ctx context.Context, instanceID, branchID, key string) (BranchOperationReceipt, error) {
	if branchID == "main" {
		return BranchOperationReceipt{}, errors.New("cannot delete the default branch")
	}
	return c.branchMutation(ctx, http.MethodDelete, instanceID, branchID, "", nil, key)
}
func (c *Client) branchMutation(ctx context.Context, method, instanceID, branchID, suffix string, body any, key string) (BranchOperationReceipt, error) {
	if !ValidResourcePathID(instanceID) || !ValidResourcePathID(branchID) || strings.TrimSpace(key) == "" {
		return BranchOperationReceipt{}, errors.New("branch target and request identity are required")
	}
	var result BranchOperationReceipt
	status, err := c.DoJSON(ctx, method, "/api/v1/instances/"+url.PathEscape(instanceID)+"/branches/"+url.PathEscape(branchID)+suffix, body, map[string]string{"Idempotency-Key": key}, &result)
	if err != nil {
		return BranchOperationReceipt{}, err
	}
	if status != http.StatusAccepted || result.InstanceID != instanceID || strings.TrimSpace(result.OperationID) == "" {
		return BranchOperationReceipt{}, errors.New("invalid branch operation receipt; outcome is unconfirmed")
	}
	return result, nil
}
