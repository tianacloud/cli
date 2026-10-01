package apppublish

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/tianacloud/cli/internal/authclient"
)

type WebProject struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	OwnerID          string `json:"owner_id"`
	TenantID         string `json:"tenant_id"`
	CreatedAt        int64  `json:"created_at"`
	CurrentVersionID string `json:"current_version_id,omitempty"`
}
type WebProjectPage struct {
	RequestID  string       `json:"-"`
	Items      []WebProject `json:"items"`
	NextCursor string       `json:"next_cursor,omitempty"`
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
func (r Runner) ListPage(ctx context.Context, after string) (WebProjectPage, *Error) {
	if after != "" && !appID(after) {
		return WebProjectPage{}, inputError("Invalid Web list cursor")
	}
	path := "/api/v1/web-projects"
	if after != "" {
		path += "?after=" + url.QueryEscape(after)
	}
	res, e := r.boundRequest(ctx, "GET", path, nil)
	if e != nil {
		return WebProjectPage{}, e
	}
	var page WebProjectPage
	if json.Unmarshal(res.Body, &page) != nil || page.Items == nil {
		return page, invalidWebResponse(res.RequestID)
	}
	last := after
	for _, w := range page.Items {
		if !appID(w.ID) || w.ID <= last || w.OwnerID != r.PrincipalID || (r.TenantID != "" && w.TenantID != r.TenantID) {
			return WebProjectPage{}, invalidWebResponse(res.RequestID)
		}
		last = w.ID
	}
	if page.NextCursor != "" && (len(page.Items) == 0 || page.NextCursor != last) {
		return WebProjectPage{}, invalidWebResponse(res.RequestID)
	}
	page.RequestID = res.RequestID
	return page, nil
}
func invalidWebResponse(requestID string) *Error {
	return &Error{RequestID: requestID, Code: "INVALID_WEB_RESPONSE", Message: "MGR returned inconsistent Web metadata", NextAction: "Inspect the service response before retrying", ExitCode: 1}
}

// Resolve uses the immutable ID first, then a complete exact-name search. Duplicate
// names never select a target, including when matches span multiple pages.
func (r Runner) Resolve(ctx context.Context, reference string) (WebProject, *Error) {
	if appID(reference) {
		res, e := r.boundRequest(ctx, "GET", "/api/v1/web-projects/"+url.PathEscape(reference), nil)
		if e == nil {
			var w WebProject
			if json.Unmarshal(res.Body, &w) != nil || w.ID != reference || w.OwnerID != r.PrincipalID || (r.TenantID != "" && w.TenantID != r.TenantID) {
				return w, invalidWebResponse(res.RequestID)
			}
			return w, nil
		}
		if e.HTTPStatus != 404 {
			return WebProject{}, e
		}

		// A durable deletion receipt survives removal from the active list and makes
		// retry by immutable ID possible after a lost DELETE response.
		receipt, de := r.Deletion(ctx, reference, nil)
		if de == nil {
			return WebProject{ID: receipt.ID, OwnerID: r.PrincipalID, TenantID: r.TenantID, CurrentVersionID: receipt.ExpectedVersionID}, nil
		}
		if de.HTTPStatus != 404 {
			return WebProject{}, de
		}

	}
	var match *WebProject
	after := ""
	lastRequestID := ""
	for page := 0; page < 10000; page++ {
		result, e := r.ListPage(ctx, after)
		if e != nil {
			return WebProject{}, e
		}
		lastRequestID = result.RequestID
		for _, w := range result.Items {
			if w.Name == reference {
				if match != nil {
					return WebProject{}, &Error{RequestID: result.RequestID, Code: "AMBIGUOUS_WEB_NAME", Message: "Multiple applications have this name", NextAction: "Use tiana web list and delete by exact ID", ExitCode: 2}
				}
				v := w
				match = &v
			}
		}
		if result.NextCursor == "" {
			if match != nil {
				return *match, nil
			}
			return WebProject{}, &Error{RequestID: result.RequestID, Code: "WEB_NOT_FOUND", Message: "application not found", NextAction: "Use tiana web list to find its ID", ExitCode: 1}
		}
		after = result.NextCursor
	}
	return WebProject{}, &Error{RequestID: lastRequestID, Code: "WEB_LIST_LIMIT", Message: "Too many Web pages to resolve safely", NextAction: "Use the exact Web ID", ExitCode: 1}
}

type WebProjectDeletion struct {
	RequestID         string `json:"-"`
	ID                string `json:"id"`
	State             string `json:"state"`
	RequestedAt       int64  `json:"requested_at"`
	DeletedAt         int64  `json:"deleted_at,omitempty"`
	ExpectedVersionID string `json:"expected_version_id"`
	GitInstanceID     string `json:"git_instance_id,omitempty"`
	SQLiteInstanceID  string `json:"sqlite_instance_id,omitempty"`
}

type WebProjectDeleteRequest struct {
	ExpectedVersionID string `json:"expected_version_id"`
	DeleteGit         bool   `json:"delete_git"`
	DeleteSQLite      bool   `json:"delete_sqlite"`
}

func (r Runner) Deletion(ctx context.Context, id string, confirmation *WebProjectDeleteRequest) (WebProjectDeletion, *Error) {
	if !appID(id) {
		return WebProjectDeletion{}, inputError("Invalid Web ID")
	}
	method, path := "GET", "/api/v1/web-projects/"+url.PathEscape(id)+"/deletion"
	var body any
	if confirmation != nil {
		method, path = "DELETE", "/api/v1/web-projects/"+url.PathEscape(id)
		body = confirmation
	}
	res, e := r.boundRequest(ctx, method, path, body)
	if e != nil {
		e.NextAction = "Inspect or retry deletion using this exact Web ID; do not resolve the name again"
		if e.HTTPStatus == 409 && e.Code == "WEB_DELETE_PREVIEW_CHANGED" {
			e.NextAction = "Review the new published version and retry deletion; this request did not start deletion"
		}
		return WebProjectDeletion{}, e
	}
	var d WebProjectDeletion
	if json.Unmarshal(res.Body, &d) != nil || d.ID != id || (d.State != "deleting" && d.State != "deleted") || d.RequestedAt <= 0 || (d.State == "deleted" && d.DeletedAt <= 0) {
		e := invalidWebResponse(res.RequestID)
		if confirmation != nil {
			e.Code = "WEB_DELETE_OUTCOME_UNKNOWN"
			e.ExitCode = 4
		}
		return WebProjectDeletion{}, e
	}
	d.RequestID = res.RequestID
	return d, nil
}
