package authclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveBranchUsesExactNameAndImmutableDefault(t *testing.T) {
	for _, name := range []string{"", "main", "开发 + %_ /&= ", "missing", "renamed"} {
		t.Run(name, func(t *testing.T) {
			lists, details := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/v1/instances/i/branches":
					lists++
					if r.URL.Query().Get("name") != name || len(r.URL.Query()) != 1 {
						t.Errorf("query=%s", r.URL.RawQuery)
					}
					page := BranchPage{Items: []Branch{}}
					if name != "missing" {
						page.Items = []Branch{{ID: "child", Name: name}}
					}
					_ = json.NewEncoder(w).Encode(page)
				case strings.HasPrefix(r.URL.Path, "/api/v1/instances/i/branches/"):
					details++
					wantID, wantName := "child", name
					if name == "" {
						wantID, wantName = "main", "production"
					}
					if !strings.HasSuffix(r.URL.Path, "/"+wantID) {
						t.Errorf("target=%s", r.URL.Path)
					}
					if name == "renamed" {
						wantName = "changed"
					}
					_ = json.NewEncoder(w).Encode(BranchDetail{InstanceID: "i", Branch: Branch{ID: wantID, Name: wantName, EndpointID: "ep-child"}, Connection: &InstanceConnection{Hostname: "ep-child.db.example", URL: "https://ep-child.db.example:9443"}})
				default:
					t.Errorf("request=%s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := testAuthenticatedClient(t, server)
			result, err := client.ResolveBranch(context.Background(), "i", name)
			wantErr := name == "missing" || name == "renamed"
			if (err != nil) != wantErr {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			wantLists, wantDetails := 1, 1
			if name == "" {
				wantLists = 0
			}
			if name == "missing" {
				wantDetails = 0
			}
			if lists != wantLists || details != wantDetails {
				t.Fatalf("calls list=%d detail=%d", lists, details)
			}
		})
	}
}
