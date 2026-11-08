package authclient

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

type CreateInstanceTokenRequest struct {
	RequestID string `json:"request_id"`
	Name      string `json:"name"`
	ExpiresAt int64  `json:"expires_at"`
}

type InstanceTokenReceipt struct {
	InstanceID        string `json:"instance_id"`
	TokenID           string `json:"token_id"`
	Name              string `json:"name"`
	Token             string `json:"token,omitempty"`
	ExpiresAt         int64  `json:"expires_at"`
	SyncStatus        string `json:"sync_status"`
	JobID             uint64 `json:"job_id,omitempty"`
	SecretRecoverable bool   `json:"secret_recoverable"`
}

func (c *Client) CreateInstanceToken(ctx context.Context, instanceID string, request CreateInstanceTokenRequest) (InstanceTokenReceipt, error) {
	if !ValidResourcePathID(instanceID) {
		return InstanceTokenReceipt{}, errors.New("invalid instance identity")
	}
	var receipt InstanceTokenReceipt
	_, err := c.DoJSON(ctx, http.MethodPost, "/api/v1/instances/"+url.PathEscape(instanceID)+"/tokens", request, nil, &receipt)
	if err != nil {
		return InstanceTokenReceipt{}, err
	}
	if receipt.InstanceID != instanceID || receipt.TokenID == "" {
		return InstanceTokenReceipt{}, errors.New("invalid token receipt; creation outcome is unconfirmed")
	}
	return receipt, nil
}
