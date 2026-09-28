package apppublish

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/tianacloud/cli/internal/authclient"
)

type App struct {
	AppID            string `json:"app_id"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	OwnerID          string `json:"owner_id"`
	TenantID         string `json:"tenant_id"`
	CreatedAt        int64  `json:"created_at"`
	CurrentVersionID string `json:"current_version_id,omitempty"`
}
type AppPage struct {
	RequestID  string `json:"-"`
	Items      []App  `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// Bind freezes account/tenant identity across pages, target resolution and confirmation.
func (r Runner) Bind(ctx context.Context) (Runner, *Error) {
	id, e := r.currentIdentity(ctx)
	if e != nil {
		return r, e
	}
	if r.PrincipalID != "" && (r.PrincipalID != id.PrincipalID || r.TenantID != id.TenantID) {
		return r, authError(authclient.ErrAuthenticationRequired)
	}
	r.PrincipalID, r.TenantID = id.PrincipalID, id.TenantID
	return r, nil
}
func (r Runner) boundRequest(ctx context.Context, method, path string, body any) (httpResult, *Error) {
	if r.PrincipalID == "" {
		return httpResult{}, authError(authclient.ErrAuthenticationRequired)
	}
	return r.request(ctx, identity{r.PrincipalID, r.TenantID}, method, path, body)
}
func (r Runner) ListPage(ctx context.Context, after string) (AppPage, *Error) {
	if after != "" && !appID(after) {
		return AppPage{}, inputError("Invalid App list cursor")
	}
	path := "/api/v1/apps"
	if after != "" {
		path += "?after=" + url.QueryEscape(after)
	}
	res, e := r.boundRequest(ctx, "GET", path, nil)
	if e != nil {
		return AppPage{}, e
	}
	var page AppPage
	if json.Unmarshal(res.Body, &page) != nil || page.Items == nil {
		return page, invalidAppResponse(res.RequestID)
	}
	last := after
	for _, w := range page.Items {
		if !appID(w.AppID) || w.AppID <= last || w.OwnerID != r.PrincipalID || (r.TenantID != "" && w.TenantID != r.TenantID) {
			return AppPage{}, invalidAppResponse(res.RequestID)
		}
		last = w.AppID
	}
	if page.NextCursor != "" && (len(page.Items) == 0 || page.NextCursor != last) {
		return AppPage{}, invalidAppResponse(res.RequestID)
	}
	page.RequestID = res.RequestID
	return page, nil
}
func invalidAppResponse(requestID string) *Error {
	return &Error{RequestID: requestID, Code: "INVALID_APP_RESPONSE", Message: "MGR returned inconsistent App metadata", NextAction: "Inspect the service response before retrying", ExitCode: 1}
}

// Resolve uses the immutable ID first, then a complete exact-name search. Duplicate
// names never select a target, including when matches span multiple pages.
func (r Runner) Resolve(ctx context.Context, reference string) (App, *Error) {
	if appID(reference) {
		res, e := r.boundRequest(ctx, "GET", "/api/v1/apps/"+url.PathEscape(reference), nil)
		if e == nil {
			var w App
			if json.Unmarshal(res.Body, &w) != nil || w.AppID != reference || w.OwnerID != r.PrincipalID || (r.TenantID != "" && w.TenantID != r.TenantID) {
				return w, invalidAppResponse(res.RequestID)
			}
			return w, nil
		}
		if e.HTTPStatus != 404 {
			return App{}, e
		}

		// A durable deletion receipt survives removal from the active list and makes
		// retry by immutable ID possible after a lost DELETE response.
		receipt, de := r.Deletion(ctx, reference, nil)
		if de == nil {
			return App{AppID: receipt.AppID, OwnerID: r.PrincipalID, TenantID: r.TenantID, CurrentVersionID: receipt.ExpectedVersionID}, nil
		}
		if de.HTTPStatus != 404 {
			return App{}, de
		}

	}
	var match *App
	after := ""
	lastRequestID := ""
	for page := 0; page < 10000; page++ {
		result, e := r.ListPage(ctx, after)
		if e != nil {
			return App{}, e
		}
		lastRequestID = result.RequestID
		for _, w := range result.Items {
			if w.Name == reference {
				if match != nil {
					return App{}, &Error{RequestID: result.RequestID, Code: "AMBIGUOUS_APP_NAME", Message: "Multiple applications have this name", NextAction: "Use tiana app list and delete by exact ID", ExitCode: 2}
				}
				v := w
				match = &v
			}
		}
		if result.NextCursor == "" {
			if match != nil {
				return *match, nil
			}
			return App{}, &Error{RequestID: result.RequestID, Code: "APP_NOT_FOUND", Message: "application not found", NextAction: "Use tiana app list to find its ID", ExitCode: 1}
		}
		after = result.NextCursor
	}
	return App{}, &Error{RequestID: lastRequestID, Code: "APP_LIST_LIMIT", Message: "Too many App pages to resolve safely", NextAction: "Use the exact App ID", ExitCode: 1}
}

type AppDeletion struct {
	RequestID         string `json:"-"`
	AppID             string `json:"app_id"`
	State             string `json:"state"`
	RequestedAt       int64  `json:"requested_at"`
	DeletedAt         int64  `json:"deleted_at,omitempty"`
	ExpectedVersionID string `json:"expected_version_id"`
	GitInstanceID     string `json:"git_instance_id,omitempty"`
	SQLiteInstanceID  string `json:"sqlite_instance_id,omitempty"`
}

type AppDeleteRequest struct {
	ExpectedVersionID string `json:"expected_version_id"`
	DeleteGit         bool   `json:"delete_git"`
	DeleteSQLite      bool   `json:"delete_sqlite"`
}

func (r Runner) Deletion(ctx context.Context, id string, confirmation *AppDeleteRequest) (AppDeletion, *Error) {
	if !appID(id) {
		return AppDeletion{}, inputError("Invalid App ID")
	}
	method, path := "GET", "/api/v1/apps/"+url.PathEscape(id)+"/deletion"
	var body any
	if confirmation != nil {
		method, path = "DELETE", "/api/v1/apps/"+url.PathEscape(id)
		body = confirmation
	}
	res, e := r.boundRequest(ctx, method, path, body)
	if e != nil {
		e.NextAction = "Inspect or retry deletion using this exact App ID; do not resolve the name again"
		if e.HTTPStatus == 409 && e.Code == "APP_DELETE_PREVIEW_CHANGED" {
			e.NextAction = "Review the new published version and retry deletion; this request did not start deletion"
		}
		return AppDeletion{}, e
	}
	var d AppDeletion
	if json.Unmarshal(res.Body, &d) != nil || d.AppID != id || (d.State != "deleting" && d.State != "deleted") || d.RequestedAt <= 0 || (d.State == "deleted" && d.DeletedAt <= 0) {
		e := invalidAppResponse(res.RequestID)
		if confirmation != nil {
			e.Code = "APP_DELETE_OUTCOME_UNKNOWN"
			e.ExitCode = 4
		}
		return AppDeletion{}, e
	}
	d.RequestID = res.RequestID
	return d, nil
}
