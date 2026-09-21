package main

import (
	"context"
	"errors"
	"strings"

	"github.com/tianacloud/cli/internal/authclient"
)

// tokenClient is the subset of the MGR client needed to create a Token and to
// read back an operation whose commit result is unknown.
type tokenClient interface {
	CreateInstanceToken(ctx context.Context, instanceID, endpointID string, request authclient.CreateTokenRequest, idempotencyKey string) (authclient.TokenResult, error)
	GetTokenOperation(ctx context.Context, operationID string) (authclient.TokenResult, error)
}

type tokenOutcome int

const (
	tokenDelivered tokenOutcome = iota
	tokenUnknown
	tokenCommittedWithoutSecret
	tokenRejected
	tokenFailed
)

// tokenAttempt is one token write with the identifiers that must stay stable
// across retries.
type tokenAttempt struct {
	InstanceID     string
	EndpointID     string
	Name           string
	ExpiresAt      int64
	IdempotencyKey string
	RequestID      string
	OperationID    string
}

type tokenResult struct {
	Outcome     tokenOutcome
	Result      authclient.TokenResult
	OperationID string
	Err         error
}

// attemptInstanceToken performs at most one write. When a previous operation
// ID is known it reads that result back first; it never issues a second write
// before the first result is known.
func attemptInstanceToken(ctx context.Context, client tokenClient, attempt tokenAttempt) tokenResult {
	if attempt.OperationID != "" {
		operation, err := client.GetTokenOperation(ctx, attempt.OperationID)
		if err != nil {
			if classifyWriteError(err) == tokenUnknown {
				return tokenResult{Outcome: tokenUnknown, OperationID: operationIDFromError(err, attempt.OperationID), Err: err}
			}
			return tokenResult{Outcome: tokenFailed, Err: err}
		}
		switch {
		case operation.CommittedWithoutSecret():
			return tokenResult{Outcome: tokenCommittedWithoutSecret, Result: operation}
		case operation.Rejected():
			return tokenResult{Outcome: tokenRejected, Result: operation}
		default:
			return tokenResult{Outcome: tokenUnknown, OperationID: attempt.OperationID, Err: errors.New("Token operation result is not yet authoritative")}
		}
	}
	result, err := client.CreateInstanceToken(ctx, attempt.InstanceID, attempt.EndpointID, authclient.CreateTokenRequest{
		RequestID: attempt.RequestID, Name: attempt.Name, ExpiresAt: attempt.ExpiresAt,
	}, attempt.IdempotencyKey)
	if err != nil {
		switch classifyWriteError(err) {
		case tokenUnknown:
			return tokenResult{Outcome: tokenUnknown, OperationID: operationIDFromError(err, ""), Err: err}
		case tokenRejected:
			return tokenResult{Outcome: tokenRejected, Err: err}
		default:
			return tokenResult{Outcome: tokenFailed, Err: err}
		}
	}
	switch {
	case result.DeliveredSecret():
		return tokenResult{Outcome: tokenDelivered, Result: result}
	case result.CommittedWithoutSecret():
		return tokenResult{Outcome: tokenCommittedWithoutSecret, Result: result}
	case result.Rejected():
		return tokenResult{Outcome: tokenRejected, Result: result}
	default:
		return tokenResult{Outcome: tokenUnknown, OperationID: result.OperationID, Err: errors.New("Token result was not confirmed")}
	}
}

// Bind only before a write. An existing operation ID is read back without
// issuing a POST, even if current instance metadata no longer has its endpoint.
func bindTokenEndpoint(saved *string, current, operationID string) error {
	if operationID != "" {
		return nil
	}
	if strings.TrimSpace(current) == "" {
		return errors.New("instance has no endpoint_id; Token creation was not attempted")
	}
	if *saved != "" && *saved != current {
		return errors.New("instance endpoint differs from pending Token target; pending operation preserved, no Token creation attempted")
	}
	*saved = current
	return nil
}

// classifyWriteError separates a request whose outcome is unknown (transport
// failure, provider outage, or an explicit unconfirmed commit) from a server
// rejection that definitively did not commit.
func classifyWriteError(err error) tokenOutcome {
	var apiErr *authclient.APIError
	if !errors.As(err, &apiErr) {
		return tokenUnknown
	}
	if apiErr.Code == "COMMIT_STATUS_UNKNOWN" || apiErr.Status >= 500 {
		return tokenUnknown
	}
	return tokenRejected
}

func operationIDFromError(err error, fallback string) string {
	var apiErr *authclient.APIError
	if errors.As(err, &apiErr) && apiErr.OperationID != "" {
		return apiErr.OperationID
	}
	return fallback
}

func tokenFailureReason(result tokenResult) string {
	if result.Result.ErrorCode != "" {
		return result.Result.ErrorCode
	}
	if result.Err != nil {
		return result.Err.Error()
	}
	return "Token write failed"
}

// saveInstanceTokenCredential stores a delivered secret and reports the
// location. A save failure does not invalidate the delivery.
func saveInstanceTokenCredential(store authclient.InstanceTokenStore, result authclient.TokenResult, instanceID string) (string, error) {
	return store.Save(authclient.InstanceTokenCredential{
		TenantID:   result.TenantID,
		InstanceID: instanceID,
		TokenID:    result.TokenID,
		Name:       result.Name,
		Token:      result.Token,
		ExpiresAt:  result.ExpiresAt,
		EndpointID: result.EndpointID,
	})
}
