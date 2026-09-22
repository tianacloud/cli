package authclient

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// InstanceDeletionReceipt means deletion was accepted, not that cleanup finished.
type InstanceDeletionReceipt struct {
	InstanceID  string `json:"instance_id"`
	OperationID string `json:"operation_id"`
}

func (c *Client) DeleteInstance(ctx context.Context, instanceID, key string) (InstanceDeletionReceipt, error) {
	if strings.TrimSpace(instanceID) == "" || instanceID == "." || instanceID == ".." || strings.ContainsAny(instanceID, "/\\\r\n\t ") || strings.TrimSpace(key) == "" {
		return InstanceDeletionReceipt{}, errors.New("instance ID and request identity are required")
	}
	var result InstanceDeletionReceipt
	status, err := c.DoJSON(ctx, http.MethodDelete, "/api/v1/instances/"+url.PathEscape(instanceID), nil, map[string]string{"Idempotency-Key": key}, &result)
	if err != nil {
		return InstanceDeletionReceipt{}, err
	}
	if status != http.StatusAccepted || result.InstanceID != instanceID || strings.TrimSpace(result.OperationID) == "" {
		return InstanceDeletionReceipt{}, errors.New("invalid instance deletion receipt; deletion outcome is unconfirmed")
	}
	return result, nil
}
