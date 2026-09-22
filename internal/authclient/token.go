package authclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
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
	JobID             uint64 `json:"job_id,omitempty"`
	Status            string `json:"status"`
	SyncStatus        string `json:"sync_status"`
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
	return r.Token == "" && r.SyncStatus == "complete"
}

// AcceptedFailed reports a persisted Token definition whose delivery job failed.
func (r TokenResult) AcceptedFailed() bool {
	return r.SyncStatus == "fail"
}

// CreateInstanceToken creates one InstanceToken. A nil error means MGR
// answered; inspect TokenResult for whether the one-time secret was delivered.
// An APIError with Code COMMIT_STATUS_UNKNOWN means the result is not yet
// authoritative and carries the operation ID to read back.
func (c *Client) CreateInstanceToken(ctx context.Context, instanceID, endpointID string, request CreateTokenRequest) (TokenResult, error) {
	if strings.TrimSpace(instanceID) == "" || strings.TrimSpace(endpointID) == "" {
		return TokenResult{}, errors.New("instance_id and endpoint_id are required for Token creation")
	}
	if strings.TrimSpace(request.RequestID) == "" {
		return TokenResult{}, errors.New("request_id is required")
	}
	path := "/api/v1/instances/" + url.PathEscape(instanceID) + "/endpoints/" + url.PathEscape(endpointID) + "/tokens"
	var response TokenResult
	status, err := c.DoJSON(ctx, http.MethodPost, path, request, nil, &response)
	if err != nil {
		return TokenResult{}, err
	}
	response.HTTPStatus = status
	return response, nil
}

// GetTokenOperation reads back a lifecycle operation without returning or
// recovering a secret.
func (c *Client) GetScopedToken(ctx context.Context, instanceID, endpointID, tokenID string) (TokenResult, error) {
	path := "/api/v1/instances/" + url.PathEscape(instanceID) + "/endpoints/" + url.PathEscape(endpointID) + "/tokens/" + url.PathEscape(tokenID)
	var response TokenResult
	status, err := c.DoJSON(ctx, http.MethodGet, path, nil, nil, &response)
	if err != nil {
		return TokenResult{}, err
	}
	response.HTTPStatus = status
	return response, nil
}

type Job struct {
	JobID          uint64 `json:"job_id"`
	RequestID      string `json:"request_id"`
	JobKind        string `json:"job_kind"`
	Status         string `json:"status"`
	RetryJobID     uint64 `json:"retry_job_id,omitempty"`
	RetryCompleted bool   `json:"retry_completed,omitempty"`
}

func (c *Client) GetJob(ctx context.Context, jobID uint64) (Job, error) {
	var response Job
	_, err := c.DoJSON(ctx, http.MethodGet, "/api/v1/jobs/"+strconv.FormatUint(jobID, 10), nil, nil, &response)
	return response, err
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
