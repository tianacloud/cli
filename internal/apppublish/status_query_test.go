package apppublish

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tianacloud/sdk-go/fetch"
)

type statusQueryFixture struct {
	t      *testing.T
	path   string
	body   string
	status int
	calls  int
}

func (s *statusQueryFixture) Close() {}
func (s *statusQueryFixture) Do(_ context.Context, r fetch.Request) (*fetch.Response, error) {
	s.calls++
	if r.Method != "GET" || r.PathQuery != s.path {
		s.t.Fatalf("unexpected control request: %s %s", r.Method, r.PathQuery)
	}
	return &fetch.Response{Status: s.status, Body: io.NopCloser(strings.NewReader(s.body))}, nil
}

// Query success and upload success must never be relabeled as activation.
func TestStatusDistinguishesCurrentContentFromReceipt(t *testing.T) {
	for _, tc := range []struct {
		name, id, path, kind, state, body string
		httpStatus                        int
	}{
		{"current", "", "/_tiana/web/current", "current", "running", `{"state":"running","publish_id":null,"remote_sha256":"target","serving_sha256":"target"}`, 200},
		{"receipt", "saved-id", "/_tiana/web/publish/saved-id", "receipt", "SUCCEEDED", `{"state":"SUCCEEDED","publish_id":"saved-id","sha256":"target","uploaded":true}`, 200},
		{"expired", "saved-id", "/_tiana/web/publish/saved-id", "receipt", "", `{"error":{"code":"PUBLISH_NOT_FOUND"}}`, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, q *http.Request) {
				switch q.URL.Path {
				case "/api/v1/web-projects/web-one":
					io.WriteString(w, `{"id":"web-one","instance_id":"web-instance","owner_id":"prn-test","tenant_id":"ten-test"}`)
				case "/api/v1/instances/web-instance":
					io.WriteString(w, `{"id":"web-instance","engine":"web","product_state":"ACTIVE","connection":{"hostname":"ep-01j5c9m7q2v8x4k6n3r0t1w2yz.example.test","url":"https://ep-01j5c9m7q2v8x4k6n3r0t1w2yz.example.test"}}`)
				default:
					t.Errorf("unexpected management request %s", q.URL.Path)
					w.WriteHeader(404)
				}
			})
			peer := &statusQueryFixture{t: t, path: tc.path, body: tc.body, status: tc.httpStatus}
			r.Fetch = func(fetch.Config) (FetchTransport, error) { return peer, nil }
			got := r.Run(t.Context(), Options{Command: "status", ID: "web-one", PublishID: tc.id})
			var kind string
			switch data := got.Data.(type) {
			case map[string]any:
				kind, _ = data["query_kind"].(string)
				if tc.state != "" && data["state"] != tc.state {
					t.Fatalf("state was relabeled: %v", data["state"])
				}
			case map[string]string:
				kind = data["query_kind"]
			}
			if kind != tc.kind {
				t.Errorf("query_kind=%q, want %q", kind, tc.kind)
			}
			if peer.calls != 1 {
				t.Fatalf("request replayed: %d", peer.calls)
			}
			if tc.name == "expired" {
				if got.Status != "unknown" || got.Error == nil || got.Error.Code != "PUBLISH_NOT_FOUND" || got.Error.ExitCode != 4 {
					t.Fatalf("expired receipt was not unknown: %+v", got)
				}
			} else if got.Status != "succeeded" || got.Error != nil {
				t.Fatalf("status failed: %+v", got)
			}
		})
	}
}

func TestStatusRejectsNullControlResponse(t *testing.T) {
	for _, id := range []string{"", "saved-id"} {
		t.Run("id="+id, func(t *testing.T) {
			r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, q *http.Request) {
				switch q.URL.Path {
				case "/api/v1/web-projects/web-one":
					io.WriteString(w, `{"id":"web-one","instance_id":"web-instance","owner_id":"prn-test","tenant_id":"ten-test"}`)
				case "/api/v1/instances/web-instance":
					io.WriteString(w, `{"id":"web-instance","engine":"web","product_state":"ACTIVE","connection":{"hostname":"ep-01j5c9m7q2v8x4k6n3r0t1w2yz.example.test","url":"https://ep-01j5c9m7q2v8x4k6n3r0t1w2yz.example.test"}}`)
				default:
					t.Errorf("unexpected management request %s", q.URL.Path)
					w.WriteHeader(404)
				}
			})
			path := "/_tiana/web/current"
			if id != "" {
				path = "/_tiana/web/publish/" + id
			}
			peer := &statusQueryFixture{t: t, path: path, body: "null", status: 200}
			r.Fetch = func(fetch.Config) (FetchTransport, error) { return peer, nil }
			got := r.Run(t.Context(), Options{Command: "status", ID: "web-one", PublishID: id})
			if got.Error == nil || got.Status != "failed" || peer.calls != 1 {
				t.Fatal("non-object status was accepted or replayed")
			}
		})
	}
}
