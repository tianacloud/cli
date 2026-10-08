package apppublish

import (
	"context"
	"encoding/json"
	"github.com/tianacloud/cli/internal/authclient"
)

func appID(s string) bool {
	if len(s) == 0 || len(s) > 80 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func appError(code, message string) *Error {
	exit := 1
	if code == "PUBLISH_OUTCOME_UNKNOWN" {
		exit = 4
	}
	return &Error{Code: code, Message: message, NextAction: "Inspect the original operation before another write", ExitCode: exit}
}

func (r Runner) Run(ctx context.Context, o Options) Result {
	if err := o.Validate(); err != nil {
		return Failure(err)
	}
	if o.Command == "publish" || o.Command == "status" {
		return r.runServerless(ctx, o)
	}
	id, e := r.currentIdentity(ctx)
	if e != nil {
		return Failure(e)
	}
	if r.PrincipalID != "" && (id.PrincipalID != r.PrincipalID || id.TenantID != r.TenantID) {
		return Failure(authError(authclient.ErrAuthenticationRequired))
	}
	if o.Command == "create" {
		if o.RequestID == "" {
			return Failure(inputError("Web creation requires a persisted request ID"))
		}
		res, e := r.request(ctx, id, "POST", "/api/v1/web-projects", struct {
			WebProjectParams
			Name        string `json:"name"`
			Description string `json:"description"`
			RequestID   string `json:"request_id"`
		}{o.WebProjectParams, o.Name, o.Description, o.RequestID})
		if e != nil {
			e.NextAction = "Repeat the same tiana web create command to recover this request"
			return Failure(e)
		}
		// Recovered creation may return edited metadata. Verify immutable identity/entry;
		// MGR validates the original request fingerprint before replaying the receipt.
		var created WebProject
		if json.Unmarshal(res.Body, &created) != nil || !appID(created.ID) || created.Entry != o.Entry || created.WebProjectParams.Validate() != nil || (created.ProjectRevision <= 1 && (created.WebProjectParams != o.WebProjectParams || created.Name != o.Name || created.Description != o.Description)) || created.OwnerID != id.PrincipalID || (id.TenantID != "" && created.TenantID != id.TenantID) {
			return Failure(&Error{RequestID: res.RequestID, Code: "CREATE_OUTCOME_UNKNOWN", Message: "MGR returned an inconsistent Web creation receipt", NextAction: "Repeat the same tiana web create command to recover the original request", ExitCode: 4})
		}
		return Success(res.Body)
	}
	return Failure(inputError("Unsupported Web command"))
}
