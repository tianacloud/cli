// Package apppublish manages Web identities and streams .tweb publications.
package apppublish

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/sdk-go/auth"
	"github.com/tianacloud/sdk-go/fetch"
)

type Runner struct {
	Fetch                 func(fetch.Config) (FetchTransport, error)
	OnPublishID           func(string)
	PersistPublication    func(context.Context, PublicationReceipt) error
	PrincipalID, TenantID string
	Client                *authclient.Client
}
type Options struct {
	WebProjectParams
	Command, ID, RequestID, Name, Description, Dir, PublishID string
}

func (o Options) Validate() *Error {
	if o.Command != "create" && !appID(o.ID) {
		return inputError("Provide a valid Web ID")
	}
	if err := o.WebProjectParams.Validate(); err != nil {
		return inputError(err.Error())
	}
	if o.Command != "create" && o.WebProjectParams != (WebProjectParams{}) {
		return inputError("project asset parameters can only be specified at create")
	}
	switch o.Command {
	case "create":
		if o.ID != "" || strings.TrimSpace(o.Name) == "" || len(o.Name) > 128 || !utf8.ValidString(o.Name) || o.Dir != "" {
			return inputError("Use web create NAME [-m DESCRIPTION] [--json]; the server generates the ID")
		}
		for _, r := range o.Name {
			if unicode.IsControl(r) {
				return inputError("Web name cannot contain control characters")
			}
		}
		if len(o.Description) > 2048 || !utf8.ValidString(o.Description) {
			return inputError("Web description must be valid UTF-8 and at most 2048 bytes")
		}
		for _, r := range o.Description {
			if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
				return inputError("Web description contains unsupported control characters")
			}
		}
	case "status":
		if o.Dir != "" || o.Name != "" || o.Description != "" || (o.PublishID != "" && !appID(o.PublishID)) {
			return inputError("Use web status ID [--publish-id ID]")
		}
	case "publish":
		if o.Dir == "" || o.Name != "" || o.Description != "" || o.PublishID != "" {
			return inputError("Use web publish ID --dir DIR")
		}
	default:
		return inputError("Choose web create, publish or status")
	}
	return nil
}

type Error struct {
	RequestID  string `json:"request_id,omitempty"`
	HTTPStatus int    `json:"-"`
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
	return &Error{Code: "INVALID_INPUT", Message: message, NextAction: "Use tiana web --help", ExitCode: 2}
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
	status, err := r.Client.RequestWebProjectJSON(ctx, method, path, body, current, &raw)
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
			message = fmt.Sprintf("MGR rejected the Web request (HTTP %d)", api.Status)
		}
		exit := 1
		if method != "GET" && api.Status >= 500 {
			exit = 4
		}
		return result, &Error{HTTPStatus: api.Status, Code: code, Message: message, NextAction: "Inspect the original operation using its immutable ID", ExitCode: exit}
	}
	if ctx.Err() != nil {
		return result, appError("UPLOAD_INTERRUPTED", "Management request interrupted; inspect the original operation")
	}
	if method != "GET" {
		return result, &Error{Code: "MGR_OUTCOME_UNKNOWN", Message: "The management write may have completed; its response was not received", NextAction: "Inspect the original operation before another write", ExitCode: 4}
	}
	return result, appError("MGR_UNAVAILABLE", "Could not reach MGR")
}
