package apppublish

import (
	"context"
	"encoding/json"
	"net/url"
)

type WebShareSettings struct {
	WebID             string          `json:"web_id"`
	URL               string          `json:"url"`
	Endpoint          string          `json:"endpoint"`
	ShareEnabled      bool            `json:"share_enabled"`
	ShareAutoApprove  bool            `json:"share_auto_approve"`
	FeatureEnabled    bool            `json:"feature_enabled"`
	CanManage         bool            `json:"can_manage"`
	CanApprove        bool            `json:"can_approve"`
	ProjectRevision   uint64          `json:"project_revision"`
	AcceptedUserCount uint64          `json:"accepted_user_count"`
	Limits            json.RawMessage `json:"limits"`
}

type WebShareUpdate struct {
	Enabled          *bool  `json:"share_enabled,omitempty"`
	AutoApprove      *bool  `json:"share_auto_approve,omitempty"`
	ExpectedRevision uint64 `json:"expected_revision"`
}

func (r Runner) Share(ctx context.Context, id string, update *WebShareUpdate) Result {
	method := "GET"
	var body any
	if update != nil {
		method, body = "POST", update
	}
	recovery := map[string]any{"web_id": id}
	if update != nil {
		recovery["requested_settings"] = update
	}
	res, e := r.boundRequest(ctx, method, "/api/v1/web-projects/"+url.PathEscape(id)+"/share", body)
	if e != nil {
		if update != nil && ctx.Err() != nil {
			e.ExitCode = 4
		}
		e.NextAction = "Run tiana web share get " + id + " --json in the original account and space before another change"
		result := Failure(e)
		result.Data = recovery
		return result
	}
	var settings WebShareSettings
	if json.Unmarshal(res.Body, &settings) != nil || settings.WebID != id || settings.ProjectRevision == 0 ||
		(update != nil && (settings.ProjectRevision <= update.ExpectedRevision ||
			(update.Enabled != nil && settings.ShareEnabled != *update.Enabled) ||
			(update.AutoApprove != nil && settings.ShareAutoApprove != *update.AutoApprove))) {
		e := invalidWebResponse(res.RequestID)
		if update != nil {
			e.Code, e.ExitCode = "WEB_SHARE_OUTCOME_UNKNOWN", 4
		}
		e.NextAction = "Run tiana web share get " + id + " --json in the original account and space"
		result := Failure(e)
		result.Data = recovery
		return result
	}
	return Success(settings)
}
