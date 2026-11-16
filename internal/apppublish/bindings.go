package apppublish

import (
	"context"
	"encoding/json"
	"net/url"
)

type WebBindings struct {
	WebID              string `json:"web_id"`
	DatabaseInstanceID string `json:"database_instance_id"`
	GitInstanceID      string `json:"git_instance_id"`
	ProjectRevision    uint64 `json:"project_revision"`
	CanManage          bool   `json:"can_manage"`
}

type WebBindingsUpdate struct {
	DatabaseInstanceID *string `json:"database_instance_id,omitempty"`
	GitInstanceID      *string `json:"git_instance_id,omitempty"`
	ExpectedRevision   uint64  `json:"expected_revision"`
}

func (r Runner) Bindings(ctx context.Context, id string, update *WebBindingsUpdate) Result {
	method := "GET"
	var body any
	if update != nil {
		method, body = "PATCH", update
	}
	recovery := map[string]any{"web_id": id}
	if update != nil {
		recovery["requested_bindings"] = update
	}
	res, e := r.boundRequest(ctx, method, "/api/v1/web-projects/"+url.PathEscape(id)+"/bindings", body)
	if e != nil {
		if update != nil && ctx.Err() != nil {
			e.ExitCode = 4
		}
		e.NextAction = "Run tiana web bindings get " + id + " --json in the original account and space before another change"
		result := Failure(e)
		result.Data = recovery
		return result
	}
	var settings WebBindings
	if json.Unmarshal(res.Body, &settings) != nil || settings.WebID != id || settings.ProjectRevision == 0 ||
		(update != nil && (settings.ProjectRevision <= update.ExpectedRevision ||
			(update.DatabaseInstanceID != nil && settings.DatabaseInstanceID != *update.DatabaseInstanceID) ||
			(update.GitInstanceID != nil && settings.GitInstanceID != *update.GitInstanceID))) {
		e := invalidWebResponse(res.RequestID)
		if update != nil {
			e.Code, e.ExitCode = "WEB_BINDINGS_OUTCOME_UNKNOWN", 4
		}
		e.NextAction = "Run tiana web bindings get " + id + " --json in the original account and space"
		result := Failure(e)
		result.Data = recovery
		return result
	}
	return Success(settings)
}
