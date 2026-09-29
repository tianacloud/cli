package apppublish

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestWebListPaginationAndExactNameResolution(t *testing.T) {
	var afters []string
	r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/v1/web-projects" {
			w.WriteHeader(404)
			return
		}
		after := req.URL.Query().Get("after")
		afters = append(afters, after)
		items := []WebProject{{ID: "web-a", Name: "相同名称", OwnerID: "prn-test", TenantID: "ten-test"}}
		cursor := "web-a"
		if after != "" {
			items = []WebProject{{ID: "web-b", Name: "相同名称", OwnerID: "prn-test", TenantID: "ten-test"}}
			cursor = ""
		}
		json.NewEncoder(w).Encode(WebProjectPage{Items: items, NextCursor: cursor})
	})
	r, e := r.Bind(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	page, e := r.ListPage(t.Context(), "")
	if e != nil || len(page.Items) != 1 || page.NextCursor != "web-a" {
		t.Fatalf("page=%+v %v", page, e)
	}
	page, e = r.ListPage(t.Context(), page.NextCursor)
	if e != nil || page.Items[0].ID != "web-b" {
		t.Fatalf("page2=%+v %v", page, e)
	}
	_, e = r.Resolve(t.Context(), "相同名称")
	if e == nil || e.Code != "AMBIGUOUS_WEB_NAME" {
		t.Fatalf("duplicate names resolved: %v", e)
	}
	if len(afters) != 4 {
		t.Fatalf("pages=%v", afters)
	}
}
func TestWebListRejectsUntrustedPagination(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"no-items", `{}`}, {"null-items", `{"items":null}`},
		{"foreign-owner", `{"items":[{"id":"web-a","owner_id":"other","tenant_id":"ten-test"}]}`},
		{"empty-loop", `{"items":[],"next_cursor":"web-a"}`},
		{"wrong-cursor", `{"items":[{"id":"web-b","owner_id":"prn-test","tenant_id":"ten-test"}],"next_cursor":"web-c"}`},
		{"backward", `{"items":[{"id":"web-a","owner_id":"prn-test","tenant_id":"ten-test"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) { w.Write([]byte(tc.body)) })
			r, e := r.Bind(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			_, e = r.ListPage(t.Context(), "web-a")
			if e == nil {
				t.Fatal("accepted invalid page")
			}
		})
	}
}
func TestWebResolvePinsIDAndDoesNotFallBackAfterServerError(t *testing.T) {
	calls := 0
	r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) { calls++; w.WriteHeader(503) })
	r, e := r.Bind(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	_, e = r.Resolve(t.Context(), "web-a")
	if e == nil || calls != 1 {
		t.Fatalf("error=%v calls=%d", e, calls)
	}
}

func TestWebResolveTwelveCharacterName(t *testing.T) {
	r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/v1/web-projects" {
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(WebProjectPage{Items: []WebProject{{ID: "AbCdEfGhIjKl", Name: "billing-prod", OwnerID: "prn-test", TenantID: "ten-test"}}})
	})
	r, e := r.Bind(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	app, e := r.Resolve(t.Context(), "billing-prod")
	if e != nil || app.ID != "AbCdEfGhIjKl" {
		t.Fatalf("app=%+v error=%v", app, e)
	}
}
