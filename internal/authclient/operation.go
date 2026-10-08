package authclient

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

// InstanceOperation is the public Control operation view proxied by MGR.
// IDs stay strings to preserve the full uint64 range.
type InstanceOperation struct {
	InstanceID     string `json:"instance_id"`
	OperationID    string `json:"operation_id"`
	BranchID       string `json:"branch_id"`
	ParentBranchID string `json:"parent_branch_id"`
	Kind           string `json:"kind"`
	State          string `json:"state"`
}

func (c *Client) GetInstanceOperation(ctx context.Context, instanceID, operationID string) (InstanceOperation, error) {
	id, err := strconv.ParseUint(operationID, 10, 64)
	if !ValidResourcePathID(instanceID) || err != nil || id == 0 {
		return InstanceOperation{}, errors.New("invalid operation identity")
	}
	var result InstanceOperation
	_, err = c.DoJSON(ctx, http.MethodGet, "/api/v1/instances/"+url.PathEscape(instanceID)+"/operations/"+url.PathEscape(operationID), nil, nil, &result)
	if err != nil {
		return InstanceOperation{}, err
	}
	if result.InstanceID != instanceID || result.OperationID != operationID {
		return InstanceOperation{}, errors.New("operation response does not match the requested instance and operation")
	}
	return result, nil
}
