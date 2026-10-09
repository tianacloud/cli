package apppublish

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/tianacloud/sdk-go/fetch"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/localfile"
	"github.com/tianacloud/cli/internal/localstate"
)

type fakeWebFetch struct {
	calls       int
	denied      bool
	unknown     bool
	lastID      string
	archivePath string
	t           *testing.T
}

func (s *fakeWebFetch) Close() {}
func (s *fakeWebFetch) Do(_ context.Context, r fetch.Request) (*fetch.Response, error) {
	s.calls++
	if file, ok := r.Body.(*os.File); ok {
		s.archivePath = file.Name()
	}
	if !strings.HasPrefix(r.PathQuery, "/_tiana/web/") {
		s.t.Fatal("reserved control path missing")
	}
	if s.unknown {
		return nil, fetch.ErrTransport
	}
	if s.denied {
		return &fetch.Response{Status: 403, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"ACCESS_DENIED"}}`))}, nil
	}
	body := map[string]any{"serving_sha256": "current"}
	if r.Method == "PUT" {
		raw, e := io.ReadAll(r.Body)
		if e != nil || int64(len(raw)) != r.BodyLength || !bytes.HasPrefix(raw, []byte("PK")) {
			s.t.Fatal("archive upload")
		}
		body = map[string]any{"uploaded": true}
		for _, h := range r.Headers {
			if h.Name == "x-tiana-publish-id" {
				s.lastID = h.Value
				body["publish_id"] = h.Value
			}
			if h.Name == "x-tiana-content-sha256" {
				body["sha256"] = h.Value
			}
		}
	}
	raw, _ := json.Marshal(body)
	return &fetch.Response{Status: 200, Body: io.NopCloser(bytes.NewReader(raw))}, nil
}
func TestServerlessPublicationUsesAccountCredentialAndDoesNotReplay(t *testing.T) {
	r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, q *http.Request) {
		switch q.URL.Path {
		case "/api/v1/web-projects/web-one":
			io.WriteString(w, `{"id":"web-one","endpoint":"https://site.example.test/web/web-one/","instance_id":"web-instance","owner_id":"prn-test","tenant_id":"ten-test"}`)
		case "/api/v1/instances/web-instance":
			io.WriteString(w, `{"id":"web-instance","engine":"web","product_state":"ACTIVE","connection":{"hostname":"ep-01j5c9m7q2v8x4k6n3r0t1w2yz.example.test","url":"https://ep-01j5c9m7q2v8x4k6n3r0t1w2yz.example.test"}}`)
		default:
			t.Errorf("unexpected MGR path %s", q.URL.Path)
			w.WriteHeader(404)
		}
	})
	fake := &fakeWebFetch{t: t}
	r.Fetch = func(c fetch.Config) (FetchTransport, error) {
		if c.Token != "private-access" {
			t.Fatal("selected account credential missing")
		}
		return fake, nil
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("site"), 0600)
	result := r.Run(t.Context(), Options{Command: "publish", ID: "web-one", Dir: dir})
	if result.Error != nil || fake.calls != 1 || fake.lastID == "" {
		t.Fatalf("publish result %+v", result)
	}
	if result.Data.(map[string]any)["endpoint"] != "https://site.example.test/web/web-one/" {
		t.Fatalf("missing hosted endpoint: %+v", result)
	}
	if _, err := os.Stat(fake.archivePath); !os.IsNotExist(err) {
		t.Fatal("confirmed upload left a temporary archive")
	}
	r.PersistPublication = func(_ context.Context, receipt PublicationReceipt) error {
		if receipt.PublishID == "" || receipt.InstanceID != "web-instance" || len(receipt.SHA256) != 64 || receipt.ArchivePath == "" {
			t.Fatal("incomplete durable receipt")
		}
		return localstate.Wrap("save publish receipt", "/synthetic/receipts/path.json", os.ErrPermission)
	}
	failed := r.Run(t.Context(), Options{Command: "publish", ID: "web-one", Dir: dir})
	if failed.Error == nil || !strings.Contains(failed.Error.Message, "/synthetic/receipts/path.json") || !strings.Contains(failed.Error.NextAction, "XDG_CONFIG_HOME") || fake.calls != 1 {
		t.Fatal("publication sent despite receipt failure")
	}
	r.PersistPublication = nil
	previous := fake.lastID
	result = r.Run(t.Context(), Options{Command: "publish", ID: "web-one", Dir: dir})
	if result.Error != nil || fake.lastID == previous {
		t.Fatal("publish identity reused")
	}
	fake.unknown = true
	result = r.Run(t.Context(), Options{Command: "publish", ID: "web-one", Dir: dir})
	if result.Status != "unknown" || result.Error.ExitCode != 4 || fake.calls != 3 || !strings.Contains(result.Error.NextAction, "--publish-id") {
		t.Fatal("uncertain request replayed or ID lost")
	}
	path := result.Data.(map[string]string)["archive_path"]
	t.Cleanup(func() { _ = os.Remove(path) })
	info, err := os.Stat(path)
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("unknown upload archive unavailable or public: %v", err)
	}
}

func TestServerlessControlUsesExplicitCredentialsWithoutFallback(t *testing.T) {
	for _, source := range []string{"endpoint", "instance", "group", "tenant", "file", "empty", "both", "bad-file"} {
		t.Run(source, func(t *testing.T) {
			for _, name := range []string{"TIANA_TOKEN", "TIANA_TOKEN_FILE"} {
				t.Setenv(name, "")
				os.Unsetenv(name)
			}
			legacy := filepath.Join(t.TempDir(), "instance-tokens.json")
			os.WriteFile(legacy, []byte("obsolete-cache-must-not-be-read"), 0600)
			t.Setenv("TIANA_INSTANCE_TOKENS_FILE", legacy)
			kind := map[string]string{"endpoint": "0", "instance": "1", "group": "2", "tenant": "3", "file": "2"}[source]
			explicit := "tia_" + kind + strings.Repeat("A", 43)
			valid := kind != ""
			switch source {
			case "file":
				file, err := localfile.CreateTemp(t.TempDir(), "credential-*")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = file.WriteString(explicit + "\n"); err != nil {
					file.Close()
					t.Fatal(err)
				}
				if err = file.Close(); err != nil {
					t.Fatal(err)
				}
				t.Setenv("TIANA_TOKEN_FILE", file.Name())
			case "empty":
				t.Setenv("TIANA_TOKEN", "")
			case "both":
				t.Setenv("TIANA_TOKEN", explicit)
				t.Setenv("TIANA_TOKEN_FILE", "missing")
			case "bad-file":
				t.Setenv("TIANA_TOKEN_FILE", "missing")
			default:
				t.Setenv("TIANA_TOKEN", explicit)
			}
			r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, q *http.Request) {
				switch q.URL.Path {
				case "/api/v1/web-projects/web-one":
					io.WriteString(w, `{"id":"web-one","instance_id":"web-instance","owner_id":"prn-test","tenant_id":"ten-test"}`)
				case "/api/v1/instances/web-instance":
					io.WriteString(w, `{"id":"web-instance","engine":"web","product_state":"ACTIVE","connection":{"hostname":"ep-01j5c9m7q2v8x4k6n3r0t1w2yz.example.test","url":"https://ep-01j5c9m7q2v8x4k6n3r0t1w2yz.example.test"}}`)
				default:
					t.Fatalf("unexpected MGR request %s", q.URL.Path)
				}
			})
			fake := &fakeWebFetch{t: t}
			factories := 0
			r.Fetch = func(c fetch.Config) (FetchTransport, error) {
				factories++
				if c.Token != explicit {
					t.Fatal("explicit credential replaced")
				}
				return fake, nil
			}
			result := r.Run(t.Context(), Options{Command: "status", ID: "web-one"})
			if (result.Error == nil) != valid || fake.calls != map[bool]int{true: 1, false: 0}[valid] {
				t.Fatal("credential precedence violated")
			}
			if valid {
				fake.denied = true
				result = r.Run(t.Context(), Options{Command: "status", ID: "web-one"})
				if result.Error == nil || fake.calls != 2 || factories != 2 {
					t.Fatal("denied token replaced or request replayed")
				}
			} else if factories != 0 {
				t.Fatal("invalid credential reached Gateway")
			}
			data, _ := os.ReadFile(legacy)
			if string(data) != "obsolete-cache-must-not-be-read" {
				t.Fatal("legacy cache modified")
			}
		})
	}
}

func TestServerlessControlRejectsAccountSwitchAndChangedTarget(t *testing.T) {
	for _, change := range []string{"user", "tenant", "instance"} {
		t.Run(change, func(t *testing.T) {
			t.Setenv("TIANA_TOKEN", "tia_2"+strings.Repeat("A", 43))
			t.Setenv("TIANA_TOKEN_FILE", "")
			os.Unsetenv("TIANA_TOKEN_FILE")
			var store authclient.CredentialStore
			credential := authclient.Credential{AccessToken: "fixture-account", RefreshToken: "fixture-refresh", ExpiresAt: time.Now().Add(time.Hour), User: authclient.User{ID: "user", TenantID: "tenant"}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch req.URL.Path {
				case "/api/v1/web-projects/web-one":
					io.WriteString(w, `{"id":"web-one","instance_id":"web-instance","owner_id":"user","tenant_id":"tenant"}`)
				case "/api/v1/instances/web-instance":
					id := "web-instance"
					switch change {
					case "user":
						credential.User.ID = "other-user"
					case "tenant":
						credential.User.TenantID = "other-tenant"
					case "instance":
						id = "different-instance"
					}
					if err := store.Save(credential); err != nil {
						t.Error(err)
					}
					json.NewEncoder(w).Encode(map[string]any{"id": id, "engine": "web", "product_state": "ACTIVE", "connection": map[string]string{"hostname": "ep-01j5c9m7q2v8x4k6n3r0t1w2yz.example.test", "url": "https://ep-01j5c9m7q2v8x4k6n3r0t1w2yz.example.test"}})
				default:
					t.Errorf("unexpected request %s", req.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			store = authclient.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"), server.URL)
			if err := store.Save(credential); err != nil {
				t.Fatal(err)
			}
			client, err := authclient.NewWithConfig(authclient.Config{Origin: server.URL, Store: store, HTTPClient: server.Client(), NonInteractive: true})
			if err != nil {
				t.Fatal(err)
			}
			runner := Runner{Client: client, Fetch: func(fetch.Config) (FetchTransport, error) {
				t.Fatal("changed account or target reached Gateway")
				return nil, nil
			}}
			if result := runner.Run(t.Context(), Options{Command: "status", ID: "web-one"}); result.Error == nil {
				t.Fatal("changed account or target accepted")
			}
		})
	}
}
