package authclient

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// CreateInstanceWithReceipt accepts both a legacy Instance response and MGR's
// asynchronous creation receipt. A queued MGR job may not have a Control
// operation ID yet. Callers persist the receipt before reporting acceptance.
func (c *Client) CreateInstanceWithReceipt(ctx context.Context, input CreateInstanceRequest, requestID string) (Instance, error) {
	if strings.TrimSpace(requestID) == "" {
		return Instance{}, errors.New("request_id is required")
	}
	var response struct {
		Instance
		AcceptedID  string `json:"instance_id"`
		OperationID string `json:"operation_id"`
		JobID       uint64 `json:"job_id"`
	}
	input.RequestID = requestID
	status, err := c.DoJSON(ctx, http.MethodPost, "/api/v1/instances", input, nil, &response)
	if err != nil {
		return Instance{}, err
	}
	if status == http.StatusAccepted {
		if strings.TrimSpace(response.AcceptedID) == "" || (strings.TrimSpace(response.OperationID) == "" && response.JobID == 0) {
			return Instance{}, errors.New("invalid instance creation receipt; retry with the same command")
		}
		response.Instance = Instance{ID: response.AcceptedID, CreationOperationID: response.OperationID, CurrentJobID: response.JobID}
	}
	if response.Instance.ID == "" {
		return Instance{}, errors.New("instance creation response has no ID; retry with the same command")
	}
	return response.Instance, nil
}
