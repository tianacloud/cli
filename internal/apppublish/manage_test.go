package apppublish

import (
	"encoding/json"
	"github.com/tianacloud/cli/internal/authclient"
	"net/http"
	"strconv"
	"testing"
)

func TestWebListPaginationAndExactNameResolution(t *testing.T) {
	var afters []string
	r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/v1/instances" {
			w.WriteHeader(404)
			return
		}
		after := req.URL.Query().Get("page")
		afters = append(afters, after)
		items := []WebProject{{ID: "web-a", Name: "相同名称", OwnerID: "prn-test", TenantID: "ten-test"}}
		if after == "2" {
			items = []WebProject{{ID: "web-b", Name: "相同名称", OwnerID: "prn-test", TenantID: "ten-test"}}
		}
		page, _ := strconv.Atoi(after)
		totalPages := 2
		writeWebInstancePage(t, w, page, totalPages, items)
	})
	r, e := r.Bind(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	page, e := r.ListPage(t.Context(), "")
	if e != nil || len(page.Items) != 1 || page.NextCursor != "2" {
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
	for _, body := range []string{`{}`, `{"page":2,"page_size":20,"items":null}`, `{"page":1,"page_size":20,"items":[]}`, `{"page":2,"page_size":20,"total_pages":3,"items":[]}`, `{"page":2,"page_size":20,"items":[{"id":"other","engine":"git"}]}`} {
		t.Run(body, func(t *testing.T) {
			r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) { w.Write([]byte(body)) })
			r, e := r.Bind(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			if _, e = r.ListPage(t.Context(), "2"); e == nil {
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
		if req.URL.Path != "/api/v1/instances" {
			w.WriteHeader(404)
			return
		}
		writeWebInstancePage(t, w, 1, 1, []WebProject{{ID: "AbCdEfGhIjKl", Name: "billing-prod"}})
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

func writeWebInstancePage(t *testing.T, w http.ResponseWriter, page, totalPages int, projects []WebProject) {
	t.Helper()
	items := []authclient.Instance{}
	for _, project := range projects {
		items = append(items, authclient.Instance{ID: "instance-" + project.ID, DisplayName: project.Name, Notes: project.Description, Engine: "web", ProductRevision: "1", CreatedAt: "2026-10-05T00:00:00Z", Web: &authclient.WebInstanceMetadata{ID: project.ID}})
	}
	if err := json.NewEncoder(w).Encode(authclient.InstancePage{Page: page, PageSize: 20, Total: totalPages, TotalPages: totalPages, Items: items}); err != nil {
		t.Fatal(err)
	}
}
