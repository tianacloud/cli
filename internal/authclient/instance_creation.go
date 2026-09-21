package authclient

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// CreateInstanceWithReceipt accepts both a legacy Instance response and MGR's
// asynchronous creation receipt. Callers persist ID before observing completion.
func (c *Client) CreateInstanceWithReceipt(ctx context.Context, input CreateInstanceRequest, key string) (Instance, error) {
	if strings.TrimSpace(key) == "" {
		return Instance{}, errors.New("Idempotency-Key is required")
	}
	var response struct {
		Instance
		AcceptedID  string `json:"instance_id"`
		OperationID string `json:"operation_id"`
	}
	status, err := c.DoJSON(ctx, http.MethodPost, "/api/v1/instances", input, map[string]string{"Idempotency-Key": key}, &response)
	if err != nil {
		return Instance{}, err
	}
	if status == http.StatusAccepted {
		if response.AcceptedID == "" || response.OperationID == "" {
			return Instance{}, errors.New("invalid instance creation receipt; retry with the same command")
		}
		response.Instance = Instance{ID: response.AcceptedID, CreationOperationID: response.OperationID}
	}
	if response.Instance.ID == "" {
		return Instance{}, errors.New("instance creation response has no ID; retry with the same command")
	}
	return response.Instance, nil
}

type InstanceOperation struct {
	InstanceID  string `json:"instance_id"`
	OperationID string `json:"operation_id"`
	Kind        string `json:"kind"`
	State       string `json:"state"`
}

func (c *Client) GetInstanceOperation(ctx context.Context, instanceID, operationID string) (InstanceOperation, error) {
	var result InstanceOperation
	_, err := c.DoJSON(ctx, http.MethodGet, "/api/v1/instances/"+url.PathEscape(instanceID)+"/operations/"+url.PathEscape(operationID), nil, nil, &result)
	if err != nil {
		return InstanceOperation{}, err
	}
	if result.InstanceID != instanceID || result.OperationID != operationID || result.Kind != "CREATE_INSTANCE" {
		return InstanceOperation{}, errors.New("creation operation does not match the requested instance")
	}
	return result, nil
}
