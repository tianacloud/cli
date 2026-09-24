package authclient

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// Branch is the CLI consumption model for MGR's public branch metadata.
type Branch struct {
	ID             string `json:"branch_id"`
	Name           string `json:"name"`
	Notes          string `json:"notes,omitempty"`
	Root           bool   `json:"root"`
	Protected      bool   `json:"protected"`
	EndpointID     string `json:"endpoint_id"`
	LifecycleState string `json:"lifecycle_state"`
	RuntimeState   string `json:"runtime_state"`
}
type BranchPage struct {
	Items      []Branch `json:"items"`
	NextCursor string   `json:"next_cursor"`
}
type BranchDetail struct {
	InstanceID string              `json:"instance_id"`
	Branch     Branch              `json:"branch"`
	Connection *InstanceConnection `json:"connection,omitempty"`
}

func (c *Client) ListBranches(ctx context.Context, instanceID, after, search, name string) (BranchPage, error) {
	query := url.Values{}
	for key, value := range map[string]string{"after": after, "search": search, "name": name} {
		if value != "" {
			query.Set(key, value)
		}
	}
	var result BranchPage
	_, err := c.DoJSON(ctx, http.MethodGet, "/api/v1/instances/"+url.PathEscape(instanceID)+"/branches?"+query.Encode(), nil, nil, &result)
	return result, err
}
func (c *Client) GetBranch(ctx context.Context, instanceID, branchID string) (BranchDetail, error) {
	var result BranchDetail
	_, err := c.DoJSON(ctx, http.MethodGet, "/api/v1/instances/"+url.PathEscape(instanceID)+"/branches/"+url.PathEscape(branchID), nil, nil, &result)
	return result, err
}

// ResolveBranch uses one indexed exact-name query. The default branch has the
// immutable ID main, regardless of its current display name.
func (c *Client) ResolveBranch(ctx context.Context, instanceID, name string) (BranchDetail, error) {
	id := "main"
	if name != "" {
		page, err := c.ListBranches(ctx, instanceID, "", "", name)
		if err != nil {
			return BranchDetail{}, err
		}
		if len(page.Items) != 1 || page.Items[0].Name != name {
			return BranchDetail{}, fmt.Errorf("branch name %q not found", name)
		}
		id = page.Items[0].ID
	}
	detail, err := c.GetBranch(ctx, instanceID, id)
	if err != nil {
		return BranchDetail{}, err
	}
	if name != "" && detail.Branch.Name != name {
		return BranchDetail{}, fmt.Errorf("branch name changed during lookup; retry the command")
	}
	return detail, nil
}
