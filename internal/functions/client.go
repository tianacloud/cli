package functions

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/tianacloud/cli/internal/authclient"
)

type Client struct {
	Account    *authclient.Client
	Credential authclient.Credential
}

func Bind(ctx context.Context, account *authclient.Client) (*Client, error) {
	c := &Client{Account: account}
	err := account.RunAuthenticated(ctx, func(_ context.Context, credential authclient.Credential) error {
		c.Credential = credential
		return nil
	})
	return c, err
}

func (c *Client) Request(ctx context.Context, method, path string, body, result any) error {
	return c.Account.RequestFunctionJSON(ctx, method, path, body, c.Credential, result)
}

func Path(id string) string         { return "/api/v1/functions/" + url.PathEscape(id) }
func InstancePath(id string) string { return "/api/v1/instances/" + url.PathEscape(id) }

func (c *Client) List(ctx context.Context, name string, page, size int) (authclient.InstancePage, error) {
	q := url.Values{"engine": {"function"}, "page": {strconv.Itoa(page)}, "page_size": {strconv.Itoa(size)}}
	if name != "" {
		q.Set("display_name", name)
	}
	var result authclient.InstancePage
	err := c.Request(ctx, "GET", "/api/v1/instances?"+q.Encode(), nil, &result)
	return result, err
}

func (c *Client) Resolve(ctx context.Context, reference string) (authclient.Instance, error) {
	var instance authclient.Instance
	err := c.Request(ctx, "GET", InstancePath(reference), nil, &instance)
	if err == nil {
		if instance.Engine != "function" {
			return instance, errors.New("instance is not a function")
		}
		return instance, nil
	}
	var api *authclient.APIError
	if !errors.As(err, &api) || (api.Code != "INSTANCE_NOT_FOUND" && api.Code != "INVALID_INSTANCE_ID") {
		return instance, err
	}
	page, err := c.List(ctx, reference, 1, 2)
	if err != nil {
		return instance, err
	}
	if page.Total > 1 {
		return instance, &authclient.DuplicateInstanceNameError{Name: reference, Count: page.Total}
	}
	if len(page.Items) == 0 {
		return instance, authclient.ErrInstanceNotFound
	}
	return page.Items[0], nil
}

type Details struct {
	SourceDir            string `json:"source_dir"`
	InstanceID           string `json:"instance_id"`
	GitInstanceID        string `json:"git_instance_id"`
	GitCreatedByFunction bool   `json:"git_created_by_function"`
	CanDeleteSourceGit   bool   `json:"can_delete_source_git"`
	ActiveSubmissionID   string `json:"active_submission_id,omitempty"`
	PendingSubmissionID  string `json:"pending_submission_id,omitempty"`
}

func (c *Client) Details(ctx context.Context, id string) (Details, error) {
	var result Details
	err := c.Request(ctx, "GET", Path(id), nil, &result)
	return result, err
}

type Submission struct {
	ID           string `json:"id"`
	Sequence     string `json:"sequence"`
	SourceCommit string `json:"source_commit"`
	BuildCommit  string `json:"build_commit,omitempty"`
	Status       string `json:"status"`
	ErrorStage   string `json:"error_stage,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
	ArtifactSize string `json:"artifact_size"`
	CreatedAt    int64  `json:"created_at"`
	FinishedAt   int64  `json:"finished_at,omitempty"`
}

type BuildPage struct {
	Items []Submission `json:"items"`
}

func (c *Client) Builds(ctx context.Context, id, before string) (BuildPage, error) {
	q := url.Values{"page_size": {"20"}}
	if before != "" {
		q.Set("before", before)
	}
	var result BuildPage
	err := c.Request(ctx, "GET", Path(id)+"/builds?"+q.Encode(), nil, &result)
	return result, err
}

func (c *Client) Build(ctx context.Context, id string, submission Submission) (Submission, error) {
	sequence, err := strconv.ParseUint(submission.Sequence, 10, 64)
	if err != nil || sequence == ^uint64(0) {
		return submission, errors.New("invalid build sequence")
	}
	page, err := c.Builds(ctx, id, strconv.FormatUint(sequence+1, 10))
	if err != nil {
		return submission, err
	}
	for _, row := range page.Items {
		if row.ID == submission.ID {
			return row, nil
		}
	}
	return submission, errors.New("build result is unavailable")
}

func (s Submission) Terminal() (bool, error) {
	switch strings.ToLower(s.Status) {
	case "published":
		return true, nil
	case "failed", "superseded", "deleted", "cancelled":
		return true, fmt.Errorf("build %s ended with %s (%s)", s.ID, s.Status, s.ErrorCode)
	}
	return false, nil
}
