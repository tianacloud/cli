package main

import (
	"context"
	"testing"

	"github.com/tianacloud/cli/internal/authclient"
)

type tokenFlowClient struct {
	created                authclient.TokenResult
	job                    authclient.Job
	resource               authclient.TokenResult
	posts, jobs, resources int
}

func (c *tokenFlowClient) CreateInstanceToken(context.Context, string, string, authclient.CreateTokenRequest) (authclient.TokenResult, error) {
	c.posts++
	return c.created, nil
}
func (c *tokenFlowClient) GetJob(context.Context, uint64) (authclient.Job, error) {
	c.jobs++
	return c.job, nil
}
func (c *tokenFlowClient) GetScopedToken(context.Context, string, string, string) (authclient.TokenResult, error) {
	c.resources++
	return c.resource, nil
}

func TestTokenAttemptRetainsInitialAcceptedFailure(t *testing.T) {
	client := &tokenFlowClient{created: authclient.TokenResult{HTTPStatus: 202, SyncStatus: "fail", JobID: 17, TokenID: "token"}}
	got := attemptInstanceToken(context.Background(), client, tokenAttempt{InstanceID: "instance", EndpointID: "endpoint", RequestID: "request"})
	if got.Outcome != tokenAcceptedFailed || got.JobID != 17 || got.TokenID != "token" || client.posts != 1 {
		t.Fatalf("outcome=%+v posts=%d", got, client.posts)
	}
}

func TestTokenAttemptObservesConsoleContinuationWithoutReplacementPost(t *testing.T) {
	client := &tokenFlowClient{
		job:      authclient.Job{JobID: 17, Status: "fail", RetryCompleted: true},
		resource: authclient.TokenResult{SyncStatus: "complete", TokenID: "token"},
	}
	got := attemptInstanceToken(context.Background(), client, tokenAttempt{InstanceID: "instance", EndpointID: "endpoint", RequestID: "request", JobID: 17, TokenID: "token"})
	if got.Outcome != tokenCommittedWithoutSecret || client.posts != 0 || client.jobs != 1 || client.resources != 1 {
		t.Fatalf("outcome=%+v calls=%d/%d/%d", got, client.posts, client.jobs, client.resources)
	}
}
