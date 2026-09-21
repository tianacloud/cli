package authclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// InstanceTokenNoExpiry is the single wire encoding for an InstanceToken with
// no product lifetime limit.
const InstanceTokenNoExpiry int64 = -1

// CreateTokenRequest is the existing MGR token creation body.
type CreateTokenRequest struct {
	RequestID string `json:"request_id"`
	Name      string `json:"name"`
	ExpiresAt int64  `json:"expires_at"`
}

// TokenResult is the CLI model shared by the creation response, an
// idempotent replay, and an operation readback. A replay or readback never
// contains Token.
type TokenResult struct {
	HTTPStatus        int    `json:"-"`
	OperationID       string `json:"operation_id"`
	Status            string `json:"status"`
	TenantID          string `json:"tenant_id"`
	InstanceID        string `json:"instance_id"`
	EndpointID        string `json:"endpoint_id"`
	TokenID           string `json:"token_id"`
	Name              string `json:"name"`
	Token             string `json:"token"`
	ExpiresAt         int64  `json:"expires_at"`
	ErrorCode         string `json:"error_code"`
	SecretRecoverable bool   `json:"secret_recoverable"`
}

// DeliveredSecret reports that this response is the one-time delivery of a
// raw Token. Only a first 201 response may satisfy this.
func (r TokenResult) DeliveredSecret() bool {
	return r.HTTPStatus == http.StatusCreated && r.Token != ""
}

// CommittedWithoutSecret reports a committed write whose raw Token cannot be
// recovered in this response.
func (r TokenResult) CommittedWithoutSecret() bool {
	return r.Token == "" && r.Status == "COMMITTED"
}

// Rejected reports a definitive lifecycle rejection. The write did not commit.
func (r TokenResult) Rejected() bool {
	return r.Status == "REJECTED"
}

// CreateInstanceToken creates one InstanceToken. A nil error means MGR
// answered; inspect TokenResult for whether the one-time secret was delivered.
// An APIError with Code COMMIT_STATUS_UNKNOWN means the result is not yet
// authoritative and carries the operation ID to read back.
func (c *Client) CreateInstanceToken(ctx context.Context, instanceID, endpointID string, request CreateTokenRequest, idempotencyKey string) (TokenResult, error) {
	if strings.TrimSpace(instanceID) == "" || strings.TrimSpace(endpointID) == "" {
		return TokenResult{}, errors.New("instance_id and endpoint_id are required for Token creation")
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return TokenResult{}, errors.New("Idempotency-Key is required")
	}
	path := "/api/v1/instances/" + url.PathEscape(instanceID) + "/endpoints/" + url.PathEscape(endpointID) + "/tokens"
	var response TokenResult
	status, err := c.DoJSON(ctx, http.MethodPost, path, request, map[string]string{"Idempotency-Key": idempotencyKey}, &response)
	if err != nil {
		return TokenResult{}, err
	}
	response.HTTPStatus = status
	return response, nil
}

// GetTokenOperation reads back a lifecycle operation without returning or
// recovering a secret.
func (c *Client) GetTokenOperation(ctx context.Context, operationID string) (TokenResult, error) {
	path := "/api/v1/gateway-auth-operations/" + url.PathEscape(operationID)
	var response TokenResult
	status, err := c.DoJSON(ctx, http.MethodGet, path, nil, nil, &response)
	if err != nil {
		return TokenResult{}, err
	}
	response.HTTPStatus = status
	return response, nil
}

// NewRequestID returns an MGR-valid request identifier for one token write.
func NewRequestID() (string, error) {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("generate request id")
	}
	return "cli-req-" + hex.EncodeToString(raw), nil
}

// NewTokenIdempotencyKey returns an idempotency key distinct from the
// instance-creation key so the two steps of a two-step create cannot collide.
func NewTokenIdempotencyKey() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("generate idempotency key")
	}
	return "cli-token-" + hex.EncodeToString(raw), nil
}
