package apppublish

import (
	"encoding/json"
	"github.com/tianacloud/sdk-go/fetch"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestWebProjectCreateFixedParameters(t *testing.T) {
	params := WebProjectParams{Entry: "assets/app.js", GitInstanceID: "git-source"}
	runner, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/web-projects" {
			t.Fatal(r.Method, r.URL)
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["entry"] != params.Entry || body["git_instance_id"] != params.GitInstanceID {
			t.Fatal(body)
		}
		for _, key := range []string{"source_commit", "styles"} {
			if _, ok := body[key]; ok {
				t.Fatal("retired field sent", key)
			}
		}
		json.NewEncoder(w).Encode(WebProject{WebProjectParams: params, ID: "web-project", Name: "Site", OwnerID: "prn-test", TenantID: "ten-test", ProjectRevision: 1})
	})
	if got := runner.Run(t.Context(), Options{Command: "create", Name: "Site", RequestID: "create-site", WebProjectParams: params}); got.Error != nil {
		t.Fatal(got.Error)
	}
}

func TestWebPublishRequiresFixedEntryAndRejectsRetiredManifest(t *testing.T) {
	for _, kind := range []string{"missing-entry", "retired-manifest", "valid"} {
		t.Run(kind, func(t *testing.T) {
			runner, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/web-projects/web-one":
					json.NewEncoder(w).Encode(WebProject{WebProjectParams: WebProjectParams{Entry: "assets/app.js"}, ID: "web-one", InstanceID: "web-instance", OwnerID: "prn-test", TenantID: "ten-test"})
				case "/api/v1/instances/web-instance":
					w.Write([]byte(`{"id":"web-instance","engine":"web","product_state":"ACTIVE","connection":{"hostname":"ep-01j5c9m7q2v8x4k6n3r0t1w2yz.example.test","url":"https://ep-01j5c9m7q2v8x4k6n3r0t1w2yz.example.test"}}`))
				default:
					t.Fatal(r.URL.Path)
				}
			})
			fake := &fakeWebFetch{t: t}
			runner.Fetch = func(fetch.Config) (FetchTransport, error) { return fake, nil }
			dir := t.TempDir()
			os.Mkdir(filepath.Join(dir, "assets"), 0700)
			if kind != "missing-entry" {
				os.WriteFile(filepath.Join(dir, "assets/app.js"), []byte("export {}"), 0600)
			}
			if kind == "retired-manifest" {
				os.WriteFile(filepath.Join(dir, "tiana.app.json"), []byte("{}"), 0600)
			}
			result := runner.Run(t.Context(), Options{Command: "publish", ID: "web-one", Dir: dir})
			if kind == "valid" {
				if result.Error != nil || fake.calls != 1 {
					t.Fatal(result)
				}
			} else if result.Error == nil || fake.calls != 0 {
				t.Fatal("invalid archive uploaded", result)
			}
		})
	}
}

func TestWebCreationReplayAcceptsLaterMetadataButNeverChangedEntry(t *testing.T) {
	for _, entry := range []string{"app.js", "different.js"} {
		t.Run(entry, func(t *testing.T) {
			runner, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(WebProject{ID: "web-project", OwnerID: "prn-test", TenantID: "ten-test", Name: "Edited", ProjectRevision: 2, WebProjectParams: WebProjectParams{Entry: entry, GitInstanceID: "changed-git"}})
			})
			got := runner.Run(t.Context(), Options{Command: "create", Name: "Original", RequestID: "original-request", WebProjectParams: WebProjectParams{Entry: "app.js"}})
			if (got.Error == nil) != (entry == "app.js") {
				t.Fatalf("replay entry %s: %+v", entry, got)
			}
		})
	}
}
