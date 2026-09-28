package apppublish

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestAppListPaginationAndExactNameResolution(t *testing.T) {
	var afters []string
	r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/v1/apps" {
			w.WriteHeader(404)
			return
		}
		after := req.URL.Query().Get("after")
		afters = append(afters, after)
		items := []App{{AppID: "apps-a", Name: "相同名称", OwnerID: "prn-test", TenantID: "ten-test"}}
		cursor := "apps-a"
		if after != "" {
			items = []App{{AppID: "apps-b", Name: "相同名称", OwnerID: "prn-test", TenantID: "ten-test"}}
			cursor = ""
		}
		json.NewEncoder(w).Encode(AppPage{Items: items, NextCursor: cursor})
	})
	r, e := r.Bind(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	page, e := r.ListPage(t.Context(), "")
	if e != nil || len(page.Items) != 1 || page.NextCursor != "apps-a" {
		t.Fatalf("page=%+v %v", page, e)
	}
	page, e = r.ListPage(t.Context(), page.NextCursor)
	if e != nil || page.Items[0].AppID != "apps-b" {
		t.Fatalf("page2=%+v %v", page, e)
	}
	_, e = r.Resolve(t.Context(), "相同名称")
	if e == nil || e.Code != "AMBIGUOUS_APP_NAME" {
		t.Fatalf("duplicate names resolved: %v", e)
	}
	if len(afters) != 4 {
		t.Fatalf("pages=%v", afters)
	}
}
func TestAppListRejectsUntrustedPagination(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"no-items", `{}`}, {"null-items", `{"items":null}`},
		{"foreign-owner", `{"items":[{"app_id":"apps-a","owner_id":"other","tenant_id":"ten-test"}]}`},
		{"empty-loop", `{"items":[],"next_cursor":"apps-a"}`},
		{"wrong-cursor", `{"items":[{"app_id":"apps-b","owner_id":"prn-test","tenant_id":"ten-test"}],"next_cursor":"apps-c"}`},
		{"backward", `{"items":[{"app_id":"apps-a","owner_id":"prn-test","tenant_id":"ten-test"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) { w.Write([]byte(tc.body)) })
			r, e := r.Bind(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			_, e = r.ListPage(t.Context(), "apps-a")
			if e == nil {
				t.Fatal("accepted invalid page")
			}
		})
	}
}
func TestAppResolvePinsIDAndDoesNotFallBackAfterServerError(t *testing.T) {
	calls := 0
	r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) { calls++; w.WriteHeader(503) })
	r, e := r.Bind(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	_, e = r.Resolve(t.Context(), "apps-a")
	if e == nil || calls != 1 {
		t.Fatalf("error=%v calls=%d", e, calls)
	}
}

func TestAppResolveTwelveCharacterName(t *testing.T) {
	r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/v1/apps" {
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(AppPage{Items: []App{{AppID: "AbCdEfGhIjKl", Name: "billing-prod", OwnerID: "prn-test", TenantID: "ten-test"}}})
	})
	r, e := r.Bind(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	app, e := r.Resolve(t.Context(), "billing-prod")
	if e != nil || app.AppID != "AbCdEfGhIjKl" {
		t.Fatalf("app=%+v error=%v", app, e)
	}
}
