// Package apppublish uploads prebuilt static files using MGR-scoped object links.
package apppublish

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/sdk-go/auth"
)

type Runner struct {
	Client     *authclient.Client
	UploadHTTP *http.Client
}
type Options struct{ Command, Project, Name, Dir, Version, Entry, UploadCAFile string }

func (o Options) Validate() *Error {
	if !appID(o.Project) {
		return inputError("Use --project with 1..80 letters, digits, hyphens or underscores")
	}
	switch o.Command {
	case "create":
		if o.Dir != "" || o.Version != "" || o.Entry != "" || o.UploadCAFile != "" {
			return inputError("apps create accepts only --project, --name and --json")
		}
	case "status":
		if !appID(o.Version) || o.Dir != "" || o.Entry != "" || o.UploadCAFile != "" || o.Name != "" {
			return inputError("Use apps status --project ID --version ID")
		}
	case "upload":
		if o.Dir == "" || (o.Version != "" && !appID(o.Version)) || o.Name != "" {
			return inputError("Use apps upload --project ID --dir DIR [--version ID] [--entry PATH]")
		}
	default:
		return inputError("Choose apps create, upload or status")
	}
	return nil
}

type Error struct {
	RequestID  string `json:"request_id,omitempty"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	NextAction string `json:"next_action"`
	ExitCode   int    `json:"-"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

type Result struct {
	Status string `json:"status"`
	Data   any    `json:"data"`
	Error  *Error `json:"error"`
}

func Success(data any) Result { return Result{Status: "succeeded", Data: data} }
func Failure(err *Error) Result {
	status := "failed"
	if err.ExitCode == 4 {
		status = "unknown"
	}
	return Result{Status: status, Error: err}
}
func inputError(message string) *Error {
	return &Error{Code: "INVALID_INPUT", Message: message, NextAction: "Use tiana apps --help", ExitCode: 2}
}
func authError(err error) *Error {
	if errors.Is(err, authclient.ErrAuthenticationRequired) || errors.Is(err, authclient.ErrCredentialNotFound) {
		return &Error{Code: "AUTH_REQUIRED", Message: "Sign in before publishing an app", NextAction: "Run tiana login", ExitCode: 5}
	}
	return &Error{Code: "CREDENTIAL_UNAVAILABLE", Message: "Cannot load or refresh the CLI credential", NextAction: "Check the current login using tiana status", ExitCode: 1}
}

type identity struct{ PrincipalID, TenantID string }

func (r Runner) currentIdentity(ctx context.Context) (identity, *Error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if r.Client == nil {
		return identity{}, authError(authclient.ErrAuthenticationRequired)
	}
	c, err := r.Client.EnsureCredential(ctx)
	if err != nil {
		return identity{}, authError(err)
	}
	if c.User.ID == "" {
		return identity{}, authError(authclient.ErrAuthenticationRequired)
	}
	return identity{c.User.ID, c.User.TenantID}, nil
}

type httpResult struct {
	RequestID string
	Status    int
	Body      json.RawMessage
}

func (r Runner) request(ctx context.Context, id identity, method, path string, body any) (result httpResult, failure *Error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	current, err := r.Client.EnsureCredential(ctx)
	if err != nil {
		return httpResult{}, authError(err)
	}
	if current.User.ID != id.PrincipalID || (id.TenantID != "" && current.User.TenantID != id.TenantID) {
		return httpResult{}, authError(authclient.ErrAuthenticationRequired)
	}
	requestID := "req-" + rand.Text()
	ctx = auth.WithRequestID(ctx, requestID)
	var raw json.RawMessage
	status, err := r.Client.RequestProjectJSON(ctx, method, path, body, current, &raw)
	result = httpResult{Status: status, Body: raw, RequestID: requestID}
	defer func() {
		if failure != nil {
			failure.RequestID = result.RequestID
		}
	}()
	if err == nil {
		return result, nil
	}
	if errors.Is(err, authclient.ErrAuthenticationRequired) {
		return result, authError(err)
	}
	var api *authclient.APIError
	if errors.As(err, &api) {
		if api.Status == http.StatusUnauthorized {
			return result, authError(authclient.ErrAuthenticationRequired)
		}
		code, message := api.Code, api.Message
		if code == "" {
			code = "MGR_REQUEST_FAILED"
		}
		if message == "" {
			message = fmt.Sprintf("MGR rejected the project request (HTTP %d)", api.Status)
		}
		exit := 1
		if method != "GET" && api.Status >= 500 {
			exit = 4
		}
		return result, &Error{Code: code, Message: message, NextAction: "Retry the same command or inspect apps status for this version", ExitCode: exit}
	}
	if ctx.Err() != nil {
		return result, appError("UPLOAD_INTERRUPTED", "Publishing was interrupted; retry the same version")
	}
	if method != "GET" {
		return result, &Error{Code: "MGR_OUTCOME_UNKNOWN", Message: "The management write may have completed; its response was not received", NextAction: "Retry the same version or inspect apps status", ExitCode: 4}
	}
	return result, appError("MGR_UNAVAILABLE", "Could not reach MGR")
}
