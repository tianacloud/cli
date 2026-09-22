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
	CreateInstanceToken(ctx context.Context, instanceID, endpointID string, request authclient.CreateTokenRequest) (authclient.TokenResult, error)
	GetScopedToken(ctx context.Context, instanceID, endpointID, tokenID string) (authclient.TokenResult, error)
	GetJob(ctx context.Context, jobID uint64) (authclient.Job, error)
}

type tokenOutcome int

const (
	tokenDelivered tokenOutcome = iota
	tokenUnknown
	tokenCommittedWithoutSecret
	tokenAcceptedFailed
	tokenRejected
	tokenFailed
)

// tokenAttempt is one token write with the identifiers that must stay stable
// across retries.
type tokenAttempt struct {
	InstanceID string
	EndpointID string
	Name       string
	ExpiresAt  int64
	RequestID  string
	JobID      uint64
	TokenID    string
}

type tokenResult struct {
	Outcome tokenOutcome
	Result  authclient.TokenResult
	JobID   uint64
	TokenID string
	Err     error
}

// attemptInstanceToken performs at most one write. When a previous operation
// ID is known it reads that result back first; it never issues a second write
// before the first result is known.
func attemptInstanceToken(ctx context.Context, client tokenClient, attempt tokenAttempt) tokenResult {
	if attempt.JobID != 0 {
		job, err := client.GetJob(ctx, attempt.JobID)
		if err != nil {
			var apiErr *authclient.APIError
			if errors.As(err, &apiErr) && apiErr.Status == 404 && attempt.TokenID != "" {
				resource, resourceErr := client.GetScopedToken(ctx, attempt.InstanceID, attempt.EndpointID, attempt.TokenID)
				if resourceErr != nil {
					return tokenResult{Outcome: tokenUnknown, JobID: attempt.JobID, TokenID: attempt.TokenID, Err: resourceErr}
				}
				if resource.CommittedWithoutSecret() {
					return tokenResult{Outcome: tokenCommittedWithoutSecret, Result: resource}
				}
			}
			return tokenResult{Outcome: tokenUnknown, JobID: attempt.JobID, TokenID: attempt.TokenID, Err: err}
		}
		if job.Status == "fail" {
			if job.RetryCompleted && attempt.TokenID != "" {
				resource, resourceErr := client.GetScopedToken(ctx, attempt.InstanceID, attempt.EndpointID, attempt.TokenID)
				if resourceErr != nil {
					return tokenResult{Outcome: tokenUnknown, JobID: attempt.JobID, TokenID: attempt.TokenID, Err: resourceErr}
				}
				if resource.CommittedWithoutSecret() {
					return tokenResult{Outcome: tokenCommittedWithoutSecret, Result: resource}
				}
			}
			return tokenResult{Outcome: tokenAcceptedFailed, JobID: job.JobID, TokenID: attempt.TokenID, Err: errors.New("Token job failed; retry it in the console")}
		}
		return tokenResult{Outcome: tokenUnknown, JobID: attempt.JobID, TokenID: attempt.TokenID, Err: errors.New("Token job is not yet complete")}
	}
	result, err := client.CreateInstanceToken(ctx, attempt.InstanceID, attempt.EndpointID, authclient.CreateTokenRequest{
		RequestID: attempt.RequestID, Name: attempt.Name, ExpiresAt: attempt.ExpiresAt,
	})
	if err != nil {
		switch classifyWriteError(err) {
		case tokenUnknown:
			return tokenResult{Outcome: tokenUnknown, Err: err}
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
	case result.AcceptedFailed():
		return tokenResult{Outcome: tokenAcceptedFailed, Result: result, JobID: result.JobID, TokenID: result.TokenID}
	default:
		return tokenResult{Outcome: tokenUnknown, JobID: result.JobID, TokenID: result.TokenID, Err: errors.New("Token result was not confirmed")}
	}
}

// Bind only before a write. An existing operation ID is read back without
// issuing a POST, even if current instance metadata no longer has its endpoint.
func bindTokenEndpoint(saved *string, current string, jobID uint64) error {
	if jobID != 0 {
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
