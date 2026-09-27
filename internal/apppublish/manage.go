package apppublish

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/tianacloud/cli/internal/authclient"
)

type Web struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	OwnerID     string `json:"owner_id"`
	TenantID    string `json:"tenant_id"`
	CreatedAt   int64  `json:"created_at"`
}
type WebPage struct {
	RequestID  string `json:"-"`
	Items      []Web  `json:"items"`
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
func (r Runner) ListPage(ctx context.Context, after string) (WebPage, *Error) {
	if after != "" && !appID(after) {
		return WebPage{}, inputError("Invalid Web list cursor")
	}
	path := "/api/v1/web-projects"
	if after != "" {
		path += "?after=" + url.QueryEscape(after)
	}
	res, e := r.boundRequest(ctx, "GET", path, nil)
	if e != nil {
		return WebPage{}, e
	}
	var page WebPage
	if json.Unmarshal(res.Body, &page) != nil || page.Items == nil {
		return page, invalidWebResponse(res.RequestID)
	}
	last := after
	for _, w := range page.Items {
		if !appID(w.ID) || w.ID <= last || w.OwnerID != r.PrincipalID || (r.TenantID != "" && w.TenantID != r.TenantID) {
			return WebPage{}, invalidWebResponse(res.RequestID)
		}
		last = w.ID
	}
	if page.NextCursor != "" && (len(page.Items) == 0 || page.NextCursor != last) {
		return WebPage{}, invalidWebResponse(res.RequestID)
	}
	page.RequestID = res.RequestID
	return page, nil
}
func invalidWebResponse(requestID string) *Error {
	return &Error{RequestID: requestID, Code: "INVALID_WEB_RESPONSE", Message: "MGR returned inconsistent Web metadata", NextAction: "Inspect the service response before retrying", ExitCode: 1}
}

// Resolve uses the immutable ID first, then a complete exact-name search. Duplicate
// names never select a target, including when matches span multiple pages.
func (r Runner) Resolve(ctx context.Context, reference string) (Web, *Error) {
	if appID(reference) {
		res, e := r.boundRequest(ctx, "GET", "/api/v1/web-projects/"+url.PathEscape(reference), nil)
		if e == nil {
			var w Web
			if json.Unmarshal(res.Body, &w) != nil || w.ID != reference || w.OwnerID != r.PrincipalID || (r.TenantID != "" && w.TenantID != r.TenantID) {
				return w, invalidWebResponse(res.RequestID)
			}
			return w, nil
		}
		if e.HTTPStatus != 404 {
			return Web{}, e
		}

		// A durable deletion receipt survives removal from the active list and makes
		// retry by immutable ID possible after a lost DELETE response.
		receipt, de := r.Deletion(ctx, reference, false)
		if de == nil {
			return Web{ID: receipt.ID, OwnerID: r.PrincipalID, TenantID: r.TenantID}, nil
		}
		if de.HTTPStatus != 404 {
			return Web{}, de
		}
		if validWebID(reference) {
			return Web{}, &Error{RequestID: de.RequestID, Code: "WEB_NOT_FOUND", Message: "Web application not found", NextAction: "Check the exact Web ID with tiana web list", ExitCode: 1}
		}
	}
	var match *Web
	after := ""
	lastRequestID := ""
	for page := 0; page < 10000; page++ {
		result, e := r.ListPage(ctx, after)
		if e != nil {
			return Web{}, e
		}
		lastRequestID = result.RequestID
		for _, w := range result.Items {
			if w.Name == reference {
				if match != nil {
					return Web{}, &Error{RequestID: result.RequestID, Code: "AMBIGUOUS_WEB_NAME", Message: "Multiple Web applications have this name", NextAction: "Use tiana web list and delete by exact ID", ExitCode: 2}
				}
				v := w
				match = &v
			}
		}
		if result.NextCursor == "" {
			if match != nil {
				return *match, nil
			}
			return Web{}, &Error{RequestID: result.RequestID, Code: "WEB_NOT_FOUND", Message: "Web application not found", NextAction: "Use tiana web list to find its ID", ExitCode: 1}
		}
		after = result.NextCursor
	}
	return Web{}, &Error{RequestID: lastRequestID, Code: "WEB_LIST_LIMIT", Message: "Too many Web pages to resolve safely", NextAction: "Use the exact Web ID", ExitCode: 1}
}

type WebDeletion struct {
	RequestID   string `json:"-"`
	ID          string `json:"id"`
	State       string `json:"state"`
	RequestedAt int64  `json:"requested_at"`
	DeletedAt   int64  `json:"deleted_at,omitempty"`
}

func (r Runner) Deletion(ctx context.Context, id string, begin bool) (WebDeletion, *Error) {
	if !appID(id) {
		return WebDeletion{}, inputError("Invalid Web ID")
	}
	method, path := "GET", "/api/v1/web-projects/"+url.PathEscape(id)+"/deletion"
	if begin {
		method, path = "DELETE", "/api/v1/web-projects/"+url.PathEscape(id)
	}
	res, e := r.boundRequest(ctx, method, path, nil)
	if e != nil {
		e.NextAction = "Inspect or retry deletion using this exact Web ID; do not resolve the name again"
		if e.HTTPStatus == 409 && e.Code == "WEB_UPLOAD_IN_PROGRESS" {
			e.NextAction = "Finish uploading and publishing before retrying deletion; this request did not start deletion"
		}
		return WebDeletion{}, e
	}
	var d WebDeletion
	if json.Unmarshal(res.Body, &d) != nil || d.ID != id || (d.State != "deleting" && d.State != "deleted") || d.RequestedAt <= 0 || (d.State == "deleted" && d.DeletedAt <= 0) {
		e := invalidWebResponse(res.RequestID)
		if begin {
			e.Code = "WEB_DELETE_OUTCOME_UNKNOWN"
			e.ExitCode = 4
		}
		return WebDeletion{}, e
	}
	d.RequestID = res.RequestID
	return d, nil
}
