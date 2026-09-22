package authclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// InstanceConnection is MGR's projection of an Endpoint into client-facing
// connection metadata. It is absent until the instance has an Endpoint.
type InstanceConnection struct {
	Hostname string `json:"hostname"`
	URL      string `json:"url"`
}

// Instance is the CLI's consumption model for the public MGR instance
// response. Unknown fields, including Web-only metadata, are ignored.
type Instance struct {
	CreationOperationID string              `json:"creation_operation_id,omitempty"`
	CurrentJobID        uint64              `json:"current_job_id,omitempty"`
	ID                  string              `json:"id"`
	DisplayName         string              `json:"display_name"`
	Engine              string              `json:"engine"`
	EndpointID          string              `json:"endpoint_id,omitempty"`
	ProductState        string              `json:"product_state"`
	DeletionPending     bool                `json:"deletion_pending"`
	LifecycleState      string              `json:"lifecycle_state,omitempty"`
	DesiredState        string              `json:"desired_state,omitempty"`
	ObservedState       string              `json:"observed_state,omitempty"`
	RuntimeStatusStale  bool                `json:"runtime_status_stale"`
	StaleReason         string              `json:"stale_reason,omitempty"`
	CreatedAt           string              `json:"created_at"`
	Connection          *InstanceConnection `json:"connection,omitempty"`
}

// InstancePage mirrors MGR's page envelope for instances.
type InstancePage struct {
	Items      []Instance `json:"items"`
	Page       int        `json:"page"`
	PageSize   int        `json:"page_size"`
	Total      int        `json:"total"`
	TotalPages int        `json:"total_pages"`
}

// DuplicateInstanceNameError reports a display name that matches more than one
// accessible instance. CandidateIDs lists the IDs on the page that proved the
// ambiguity so the caller can retry with an explicit ID.
type DuplicateInstanceNameError struct {
	Name         string
	Count        int
	CandidateIDs []string
}

func (e *DuplicateInstanceNameError) Error() string {
	return fmt.Sprintf("instance name %q matches %d instances", e.Name, e.Count)
}

func (c *Client) CreateInstance(ctx context.Context, input CreateInstanceRequest, requestID string) (Instance, error) {
	return c.CreateInstanceWithReceipt(ctx, input, requestID)
}

func (c *Client) GetInstance(ctx context.Context, instanceID string) (Instance, error) {
	var response Instance
	_, err := c.DoJSON(ctx, http.MethodGet, "/api/v1/instances/"+url.PathEscape(instanceID), nil, nil, &response)
	if err != nil {
		return Instance{}, instanceLookupError(err)
	}
	return response, nil
}

// ListInstances reads one page of the authenticated Tenant's instances. A
// non-empty displayName asks MGR to filter the whole Tenant collection before
// pagination; empty preserves the unfiltered listing.
func (c *Client) ListInstances(ctx context.Context, displayName string, page, pageSize int) (InstancePage, error) {
	query := url.Values{}
	if strings.TrimSpace(displayName) != "" {
		query.Set("display_name", strings.TrimSpace(displayName))
	}
	query.Set("page", strconv.Itoa(page))
	query.Set("page_size", strconv.Itoa(pageSize))
	var response InstancePage
	_, err := c.DoJSON(ctx, http.MethodGet, "/api/v1/instances?"+query.Encode(), nil, nil, &response)
	if err != nil {
		return InstancePage{}, err
	}
	return response, nil
}

// ResolveInstance locates one instance from an ID or an exact display name.
// The ID interpretation is always attempted first; a name query runs only
// when MGR explicitly rejects the ID or reports it missing. Transport,
// authentication, and service failures are returned unchanged.
func (c *Client) ResolveInstance(ctx context.Context, reference string) (Instance, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return Instance{}, ErrInstanceInvalidID
	}
	instance, err := c.GetInstance(ctx, reference)
	if err == nil {
		return instance, nil
	}
	if !errors.Is(err, ErrInstanceNotFound) && !errors.Is(err, ErrInstanceInvalidID) {
		return Instance{}, err
	}
	page, err := c.ListInstances(ctx, reference, 1, 20)
	if err != nil {
		return Instance{}, err
	}
	if page.Total > 1 {
		candidates := make([]string, 0, len(page.Items))
		for _, item := range page.Items {
			candidates = append(candidates, item.ID)
		}
		return Instance{}, &DuplicateInstanceNameError{Name: reference, Count: page.Total, CandidateIDs: candidates}
	}
	if page.Total == 0 || len(page.Items) == 0 {
		return Instance{}, ErrInstanceNotFound
	}
	return page.Items[0], nil
}

func instanceLookupError(err error) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch {
	case apiErr.Status == http.StatusNotFound && apiErr.Code == "INSTANCE_NOT_FOUND":
		return ErrInstanceNotFound
	case apiErr.Status == http.StatusBadRequest && apiErr.Code == "INVALID_INSTANCE_ID":
		return ErrInstanceInvalidID
	}
	return err
}
