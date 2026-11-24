package apppublish

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/sdk-go/fetch"
)

type fakePullFetch struct {
	calls int
	doFn  func(context.Context, fetch.Request) (*fetch.Response, error)
}

func (f *fakePullFetch) Close() {}
func (f *fakePullFetch) Do(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
	f.calls++
	return f.doFn(ctx, req)
}

func setupPullRunner(t *testing.T, entry string, doFn func(context.Context, fetch.Request) (*fetch.Response, error)) (Runner, *fakePullFetch, *int) {
	t.Helper()
	mgrCalls := 0
	r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, q *http.Request) {
		mgrCalls++
		switch q.URL.Path {
		case "/api/v1/web-projects/web-one":
			payload := map[string]any{
				"id":          "web-one",
				"endpoint":    "https://site.example.test/web/web-one/",
				"instance_id": "web-instance",
				"owner_id":    "prn-test",
				"tenant_id":   "ten-test",
			}
			if entry != "" {
				payload["entry"] = entry
			}
			_ = json.NewEncoder(w).Encode(payload)
		case "/api/v1/instances/web-instance":
			_, _ = io.WriteString(w, `{"id":"web-instance","engine":"web","product_state":"ACTIVE","connection":{"hostname":"ep-01j5c9m7q2v8x4k6n3r0t1w2yz.example.test","url":"https://ep-01j5c9m7q2v8x4k6n3r0t1w2yz.example.test"}}`)
		default:
			t.Errorf("unexpected MGR path %s", q.URL.Path)
			w.WriteHeader(404)
		}
	})
	fake := &fakePullFetch{doFn: doFn}
	r.Fetch = func(c fetch.Config) (FetchTransport, error) {
		if c.Token != "private-access" {
			t.Fatalf("unexpected credential token %q", c.Token)
		}
		return fake, nil
	}
	return r, fake, &mgrCalls
}

func TestWebPullRoundTripAndJSONOutput(t *testing.T) {
	for _, tc := range []struct {
		name         string
		entry        string
		files        map[string]string
		looksBundled bool
	}{
		{
			name:  "unbundled source app",
			entry: "app.js",
			files: map[string]string{
				"index.html":    "<!doctype html><html><body>Hello</body></html>\n",
				"app.js":        "export function main() {\n  return 42;\n}\n",
				"css/style.css": "body { margin: 0; }\n",
			},
			looksBundled: false,
		},
		{
			name:  "bundled vite output",
			entry: "app.js",
			files: map[string]string{
				"index.html":               "<!doctype html><script src=\"/assets/index-AbCd1234.js\"></script>\n",
				"app.js":                   "export default 1;\n",
				"assets/index-AbCd1234.js": "console.log('bundled');\n",
			},
			looksBundled: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := t.TempDir()
			var expectedBytes int64
			for rel, content := range tc.files {
				expectedBytes += int64(len(content))
				full := filepath.Join(src, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			archiveBytes, archiveSha, err := Pack(src)
			if err != nil {
				t.Fatal(err)
			}

			r, fake, _ := setupPullRunner(t, tc.entry, func(_ context.Context, req fetch.Request) (*fetch.Response, error) {
				if req.Method != "GET" || req.PathQuery != "/_tiana/web/archive" || req.BodyLength != 0 {
					t.Fatalf("unexpected fetch request: %+v", req)
				}
				hdr := http.Header{}
				hdr.Set("Content-Type", "application/zip")
				hdr.Set("X-Tiana-Web-Sha256", archiveSha)
				return &fetch.Response{
					Status: http.StatusOK,
					Header: hdr,
					Body:   io.NopCloser(bytes.NewReader(archiveBytes)),
				}, nil
			})

			parent := t.TempDir()
			dest := filepath.Join(parent, "pulled-app")
			res := r.Run(t.Context(), Options{Command: "pull", ID: "web-one", Dir: dest})
			if res.Error != nil || res.Status != "succeeded" || fake.calls != 1 {
				t.Fatalf("pull failed: %+v (calls=%d)", res, fake.calls)
			}

			rawJSON, err := json.Marshal(res)
			if err != nil {
				t.Fatal(err)
			}
			var envelope struct {
				Status string         `json:"status"`
				Data   map[string]any `json:"data"`
				Error  any            `json:"error"`
			}
			if err := json.Unmarshal(rawJSON, &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Status != "succeeded" || envelope.Error != nil {
				t.Fatalf("unexpected envelope: %s", rawJSON)
			}
			absDest, _ := filepath.Abs(dest)
			if envelope.Data["web_id"] != "web-one" ||
				envelope.Data["instance_id"] != "web-instance" ||
				envelope.Data["dir"] != absDest ||
				envelope.Data["serving_sha256"] != archiveSha ||
				envelope.Data["entry"] != tc.entry ||
				int(envelope.Data["files_count"].(float64)) != len(tc.files) ||
				int64(envelope.Data["total_bytes"].(float64)) != expectedBytes ||
				envelope.Data["looks_bundled"].(bool) != tc.looksBundled {
				t.Fatalf("unexpected JSON data: %s", rawJSON)
			}

			for rel, want := range tc.files {
				got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(rel)))
				if err != nil || string(got) != want {
					t.Fatalf("extracted %s: got %q err=%v want %q", rel, got, err, want)
				}
			}
			if _, err := os.Lstat(filepath.Join(dest, "web.yaml")); !os.IsNotExist(err) {
				t.Fatal("web.yaml should not be extracted")
			}
			assertNoLeftoverTempEntries(t, parent, "pulled-app")
		})
	}
}

func TestWebPullErrorHandlingAndStatusMapping(t *testing.T) {
	validArchive := buildCustomZip(t, []rawZipEntry{
		{name: "web.yaml"},
		{name: "data/index.html", content: []byte("ok")},
	})
	sum := sha256.Sum256(validArchive)
	validSha := hex.EncodeToString(sum[:])

	cases := []struct {
		name       string
		doFn       func(context.Context, fetch.Request) (*fetch.Response, error)
		wantCode   string
		wantExit   int
		wantStatus int
	}{
		{
			name: "404 NOT_FOUND maps to ARCHIVE_ENDPOINT_UNSUPPORTED",
			doFn: func(context.Context, fetch.Request) (*fetch.Response, error) {
				return &fetch.Response{Status: 404, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"NOT_FOUND","message":"not found"}}`))}, nil
			},
			wantCode:   "ARCHIVE_ENDPOINT_UNSUPPORTED",
			wantExit:   1,
			wantStatus: 404,
		},
		{
			name: "404 ARCHIVE_NOT_FOUND maps to ARCHIVE_NOT_FOUND",
			doFn: func(context.Context, fetch.Request) (*fetch.Response, error) {
				return &fetch.Response{Status: 404, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"ARCHIVE_NOT_FOUND","message":"no snapshot"}}`))}, nil
			},
			wantCode:   "ARCHIVE_NOT_FOUND",
			wantExit:   1,
			wantStatus: 404,
		},
		{
			name: "403 SITE_CALLER_FORBIDDEN maps to auth error exit 5",
			doFn: func(context.Context, fetch.Request) (*fetch.Response, error) {
				return &fetch.Response{Status: 403, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"SITE_CALLER_FORBIDDEN","message":"site caller forbidden"}}`))}, nil
			},
			wantCode:   "SITE_CALLER_FORBIDDEN",
			wantExit:   5,
			wantStatus: 403,
		},
		{
			name: "401 unauthorized response maps to auth error exit 5",
			doFn: func(context.Context, fetch.Request) (*fetch.Response, error) {
				return &fetch.Response{Status: 401, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			},
			wantCode:   "AUTH_REQUIRED",
			wantExit:   5,
			wantStatus: 401,
		},
		{
			name: "gateway fetch.Error 403 maps to auth error exit 5",
			doFn: func(context.Context, fetch.Request) (*fetch.Response, error) {
				return nil, &fetch.Error{Status: 403, Code: "ACCESS_DENIED", RequestID: "req-gw"}
			},
			wantCode:   "ACCESS_DENIED",
			wantExit:   5,
			wantStatus: 403,
		},
		{
			name: "gateway fetch.Error 401 without code maps to AUTH_REQUIRED exit 5",
			doFn: func(context.Context, fetch.Request) (*fetch.Response, error) {
				return nil, &fetch.Error{Status: 401}
			},
			wantCode:   "AUTH_REQUIRED",
			wantExit:   5,
			wantStatus: 401,
		},
		{
			name: "503 APP_NOT_ACCEPTING parsed cleanly",
			doFn: func(context.Context, fetch.Request) (*fetch.Response, error) {
				return &fetch.Response{Status: 503, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"APP_NOT_ACCEPTING","message":"maintenance"}}`))}, nil
			},
			wantCode:   "APP_NOT_ACCEPTING",
			wantExit:   1,
			wantStatus: 503,
		},
		{
			name: "oversized error body bounded at 64 KiB",
			doFn: func(context.Context, fetch.Request) (*fetch.Response, error) {
				return &fetch.Response{Status: 500, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 100000)))}, nil
			},
			wantCode:   "WEB_CONTROL_FAILED",
			wantExit:   1,
			wantStatus: 500,
		},
		{
			name: "missing X-Tiana-Web-Sha256 header",
			doFn: func(context.Context, fetch.Request) (*fetch.Response, error) {
				return &fetch.Response{Status: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(validArchive))}, nil
			},
			wantCode: "INVALID_ARCHIVE_RESPONSE",
			wantExit: 1,
		},
		{
			name: "uppercase X-Tiana-Web-Sha256 header rejected",
			doFn: func(context.Context, fetch.Request) (*fetch.Response, error) {
				h := http.Header{}
				h.Set("X-Tiana-Web-Sha256", strings.ToUpper(validSha))
				return &fetch.Response{Status: 200, Header: h, Body: io.NopCloser(bytes.NewReader(validArchive))}, nil
			},
			wantCode: "INVALID_ARCHIVE_RESPONSE",
			wantExit: 1,
		},
		{
			name: "digest mismatch cleans temp file",
			doFn: func(context.Context, fetch.Request) (*fetch.Response, error) {
				h := http.Header{}
				h.Set("X-Tiana-Web-Sha256", strings.Repeat("0", 64))
				return &fetch.Response{Status: 200, Header: h, Body: io.NopCloser(bytes.NewReader(validArchive))}, nil
			},
			wantCode: "ARCHIVE_DIGEST_MISMATCH",
			wantExit: 1,
		},
		{
			name: "response body exceeding 16 MiB rejected and cleaned",
			doFn: func(context.Context, fetch.Request) (*fetch.Response, error) {
				h := http.Header{}
				h.Set("X-Tiana-Web-Sha256", validSha)
				return &fetch.Response{Status: 200, Header: h, Body: io.NopCloser(bytes.NewReader(make([]byte, maxWebBytes+1)))}, nil
			},
			wantCode: "ARCHIVE_TOO_LARGE",
			wantExit: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _, _ := setupPullRunner(t, "", tc.doFn)
			parent := t.TempDir()
			dest := filepath.Join(parent, "dest")
			res := r.Run(t.Context(), Options{Command: "pull", ID: "web-one", Dir: dest})
			if res.Error == nil {
				t.Fatalf("expected error %s, got success: %+v", tc.wantCode, res)
			}
			if res.Error.Code != tc.wantCode || res.Error.ExitCode != tc.wantExit {
				t.Fatalf("got error %+v, want code=%s exit=%d", res.Error, tc.wantCode, tc.wantExit)
			}
			if tc.wantStatus != 0 && res.Error.HTTPStatus != tc.wantStatus {
				t.Fatalf("got HTTPStatus=%d want %d", res.Error.HTTPStatus, tc.wantStatus)
			}
			if _, err := os.Lstat(dest); !os.IsNotExist(err) {
				t.Fatalf("destination %s must not exist on failure", dest)
			}
			assertNoLeftoverTempEntries(t, parent)
		})
	}
}

func TestWebPullPreNetworkChecksFailBeforeNetwork(t *testing.T) {
	r, fake, mgrCalls := setupPullRunner(t, "", func(context.Context, fetch.Request) (*fetch.Response, error) {
		t.Fatal("fetch must not be called")
		return nil, nil
	})

	// 1. Existing directory leaves target untouched and does zero network calls.
	parent := t.TempDir()
	existing := filepath.Join(parent, "existing")
	if err := os.Mkdir(existing, 0755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(existing, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("stay"), 0644); err != nil {
		t.Fatal(err)
	}
	res := r.Run(t.Context(), Options{Command: "pull", ID: "web-one", Dir: existing})
	if res.Error == nil || res.Error.Code != "TARGET_ALREADY_EXISTS" || res.Error.ExitCode != 2 {
		t.Fatalf("unexpected existing-target result: %+v", res)
	}
	if *mgrCalls != 0 || fake.calls != 0 {
		t.Fatalf("pre-network check hit network: mgr=%d fetch=%d", *mgrCalls, fake.calls)
	}
	got, err := os.ReadFile(sentinel)
	if err != nil || string(got) != "stay" {
		t.Fatalf("existing target altered: %q %v", got, err)
	}
	assertNoLeftoverTempEntries(t, parent, "existing")

	// 2. Missing parent directory fails before network.
	missingParentDest := filepath.Join(parent, "does-not-exist", "dest")
	res = r.Run(t.Context(), Options{Command: "pull", ID: "web-one", Dir: missingParentDest})
	if res.Error == nil || res.Error.Code != "INVALID_INPUT" || res.Error.ExitCode != 2 {
		t.Fatalf("unexpected missing-parent result: %+v", res)
	}
	if *mgrCalls != 0 || fake.calls != 0 {
		t.Fatalf("missing-parent check hit network: mgr=%d fetch=%d", *mgrCalls, fake.calls)
	}
}

func TestWebPullRejectsAccountSwitchBeforeFetch(t *testing.T) {
	var store authclient.CredentialStore
	credential := authclient.Credential{
		AccessToken:  "fixture-account",
		RefreshToken: "fixture-refresh",
		ExpiresAt:    time.Now().Add(time.Hour),
		User:         authclient.User{ID: "user", TenantID: "tenant"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/api/v1/web-projects/web-one":
			_, _ = io.WriteString(w, `{"id":"web-one","instance_id":"web-instance","owner_id":"user","tenant_id":"tenant"}`)
		case "/api/v1/instances/web-instance":
			credential.User.TenantID = "other-tenant"
			if err := store.Save(credential); err != nil {
				t.Error(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":            "web-instance",
				"engine":        "web",
				"product_state": "ACTIVE",
				"connection":    map[string]string{"hostname": "ep-01j5c9m7q2v8x4k6n3r0t1w2yz.example.test", "url": "https://ep-01j5c9m7q2v8x4k6n3r0t1w2yz.example.test"},
			})
		default:
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
	runner := Runner{
		Client: client,
		Fetch: func(fetch.Config) (FetchTransport, error) {
			t.Fatal("account switch reached Gateway")
			return nil, nil
		},
	}
	dest := filepath.Join(t.TempDir(), "dest")
	res := runner.Run(t.Context(), Options{Command: "pull", ID: "web-one", Dir: dest})
	if res.Error == nil || res.Error.ExitCode != 5 {
		t.Fatalf("expected auth failure on account switch, got %+v", res)
	}
}
