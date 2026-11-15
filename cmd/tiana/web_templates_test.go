package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/tianacloud/cli/internal/authclient"
	"strings"
	"testing"
)

func TestWebTemplateCommandSurface(t *testing.T) {
	for _, command := range []string{"list-template", "init-template", "status-template", "status-share-template", "publish-share-template", "set-template-distribution", "delete-template", "init-unified-template", "list-owned-templates"} {
		var out, diagnostics bytes.Buffer
		if code := runCLI(context.Background(), []string{"web", command, "--help"}, strings.NewReader(""), &out, &diagnostics); code != 0 || !strings.Contains(out.String(), command) {
			t.Fatalf("%s help: %d %s %s", command, code, &out, &diagnostics)
		}
	}
	for _, args := range [][]string{{"web", "init-template", "notes"}, {"web", "init-template", "--dir", "target"}, {"web", "list-template", "notes"}, {"web", "status-template"}, {"web", "set-template-distribution", "template-one"}} {
		var out, diagnostics bytes.Buffer
		if code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diagnostics); code != 2 {
			t.Fatalf("invalid args %v: %d %s %s", args, code, &out, &diagnostics)
		}
	}
}

func TestUnifiedDistributionUnknownPreservesOriginalRequest(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/templates/tpl-1/distribution" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_test")
	var out, diag bytes.Buffer
	code := runCLI(context.Background(), []string{"web", "set-template-distribution", "tpl-1", "--request-id", "req-1", "--expected-revision", "9007199254740993", "--share-enabled=false", "--json"}, strings.NewReader(""), &out, &diag)
	for _, part := range []string{`"status":"unknown"`, `"template_id":"tpl-1"`, `"request_id":"req-1"`, `"expected_revision":"9007199254740993"`, `"share_enabled":false`} {
		if !strings.Contains(out.String(), part) {
			t.Fatalf("missing %s: %d %s %s", part, code, out.String(), diag.String())
		}
	}
	if code != 4 || calls != 1 || strings.Contains(out.String(), `"market_enabled"`) {
		t.Fatalf("code=%d calls=%d out=%s", code, calls, out.String())
	}
}

func TestUnifiedWriteBlockedByExistingPending(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_test")
	if err := authclient.NewFilePendingCommandStore(env.pendingPath).Save(authclient.PendingCommand{Command: "db.create", Origin: server.URL, IdempotencyKey: "pending"}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "install.zip")
	if err := os.WriteFile(file, []byte("reviewed"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"web", "set-template-distribution", "tpl-1", "--request-id", "req-1", "--expected-revision", "1", "--share-enabled=true", "--json"}, {"web", "publish-share-template", "source-web", "--unified", "--request-id", "req-1", "--file", file, "--json"}} {
		var out, diag bytes.Buffer
		if code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diag); code == 0 {
			t.Fatalf("pending allowed write: %v %s", args, out.String())
		}
	}
	if calls != 0 {
		t.Fatalf("made %d requests", calls)
	}
}

func TestUnifiedPublicationWaitFailureKeepsAcceptedJob(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/templates":
			io.WriteString(w, `{"template_id":"tpl-1","source_web_id":"source-web","distribution":{"share_enabled":false,"market_enabled":false}}`)
		case "/api/v1/templates/tpl-1/uploads":
			io.WriteString(w, `{"template_id":"tpl-1","job_id":7,"status":"pending","published_version":null}`)
		case "/api/v1/templates/tpl-1/publications/7":
			w.WriteHeader(503)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_test")
	file := filepath.Join(t.TempDir(), "install.zip")
	if err := os.WriteFile(file, []byte("reviewed"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	code := runCLI(context.Background(), []string{"web", "publish-share-template", "source-web", "--unified", "--request-id", "req-1", "--file", file, "--wait", "--json"}, strings.NewReader(""), &out, &diag)
	for _, part := range []string{`"status":"unknown"`, `"template_id":"tpl-1"`, `"request_id":"req-1"`, `"job_id":7`} {
		if !strings.Contains(out.String(), part) {
			t.Fatalf("missing %s: %d %s %s", part, code, out.String(), diag.String())
		}
	}
	if code != 4 {
		t.Fatalf("code=%d out=%s diag=%s", code, out.String(), diag.String())
	}
}

func TestUnifiedPublicationAcceptedFailedAndCancelled(t *testing.T) {
	for _, mode := range []string{"accepted", "failed", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/templates":
					io.WriteString(w, `{"template_id":"tpl-1","source_web_id":"source-web","distribution":{"share_enabled":false,"market_enabled":false}}`)
				case "/api/v1/templates/tpl-1/uploads":
					if mode == "failed" {
						io.WriteString(w, `{"template_id":"tpl-1","job_id":7,"status":"fail","last_error":"TEMPLATE_PACKAGE_INVALID: missing showcase/cover.webp","published_version":null}`)
					} else {
						io.WriteString(w, `{"template_id":"tpl-1","job_id":7,"status":"pending","published_version":null}`)
					}
				case "/api/v1/templates/tpl-1/publications/7":
					cancel()
				default:
					t.Errorf("unexpected %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_test")
			file := filepath.Join(t.TempDir(), "install.zip")
			if err := os.WriteFile(file, []byte("reviewed"), 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"web", "publish-share-template", "source-web", "--unified", "--request-id", "req-1", "--file", file, "--json"}
			if mode == "cancelled" {
				args = append(args, "--wait")
			}
			var out, diag bytes.Buffer
			code := runCLI(ctx, args, strings.NewReader(""), &out, &diag)
			wantCode, wantStatus := 0, `"status":"accepted"`
			if mode == "failed" {
				wantCode, wantStatus = 1, `"status":"failed"`
			}
			if mode == "cancelled" {
				wantCode, wantStatus = 130, `"status":"unknown"`
			}
			if mode == "failed" && !strings.Contains(out.String(), `"last_error":"TEMPLATE_PACKAGE_INVALID: missing showcase/cover.webp"`) {
				t.Fatalf("missing last_error: %s", out.String())
			}
			if code != wantCode || !strings.Contains(out.String(), wantStatus) || !strings.Contains(out.String(), `"job_id":7`) {
				t.Fatalf("mode=%s code=%d out=%s diag=%s", mode, code, out.String(), diag.String())
			}
		})
	}
}

func TestUnifiedCreateInvalidReceiptIsUnknownWithoutForeignIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"template_id":"foreign-id","source_web_id":"other-web","distribution":{"share_enabled":false,"market_enabled":false}}`)
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_test")
	file := filepath.Join(t.TempDir(), "install.zip")
	if err := os.WriteFile(file, []byte("reviewed"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	code := runCLI(context.Background(), []string{"web", "publish-share-template", "source-web", "--unified", "--request-id", "req-1", "--file", file, "--json"}, strings.NewReader(""), &out, &diag)
	if code != 4 || !strings.Contains(out.String(), `"status":"unknown"`) || !strings.Contains(out.String(), `"request_id":"req-1"`) || strings.Contains(out.String(), "foreign-id") {
		t.Fatalf("code=%d out=%s diag=%s", code, out.String(), diag.String())
	}
}

func TestUnifiedInvalidArgumentsFailBeforeNetwork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected network request") }))
	defer server.Close()
	newTestEnv(t, server.URL)
	for _, args := range [][]string{
		{"web", "set-template-distribution", "tpl-1", "--request-id", "  ", "--expected-revision", "1", "--share-enabled=true", "--json"},
		{"web", "set-template-distribution", "tpl-1", "--request-id", "req", "--expected-revision", "abc", "--share-enabled=true", "--json"},
		{"web", "set-template-distribution", "../bad", "--request-id", "req", "--expected-revision", "1", "--share-enabled=true", "--json"},
		{"web", "status-template", "tpl-1", "--job-id", "0", "--json"},
		{"web", "init-unified-template", "tpl-1", "--dir", "target", "--channel", "wrong", "--json"},
	} {
		var out, diag bytes.Buffer
		if code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diag); code != 2 {
			t.Fatalf("args=%v code=%d out=%s diag=%s", args, code, out.String(), diag.String())
		}
	}
}

func TestUnifiedResultUnknownIsNotDefiniteRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		io.WriteString(w, `{"error":{"code":"RESULT_UNKNOWN","message":"uncertain"}}`)
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_test")
	var out, diag bytes.Buffer
	code := runCLI(context.Background(), []string{"web", "set-template-distribution", "tpl-1", "--request-id", "req", "--expected-revision", "1", "--share-enabled=true", "--json"}, strings.NewReader(""), &out, &diag)
	if code != 4 || !strings.Contains(out.String(), `"status":"unknown"`) {
		t.Fatalf("code=%d out=%s diag=%s", code, out.String(), diag.String())
	}
}

func TestUnifiedSingleEntryCatalogStatusAndDeleteCommands(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/template-catalog":
			if r.URL.Query().Get("channel") != "market" || r.URL.Query().Get("q") != "notes" {
				t.Errorf("unexpected catalog query: %s", r.URL.RawQuery)
			}
			io.WriteString(w, `{"items":[{"template_id":"tpl-notes","source_web_id":"web-notes","display_name":"Notes","description":"unified notes","distribution":{"share_enabled":true,"market_enabled":true},"current_version":{"version":"1","has_source":true,"installable":true}}],"next_cursor":""}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/templates" && r.URL.Query().Get("source_web_id") == "web-notes":
			io.WriteString(w, `{"items":[{"template_id":"tpl-notes","source_web_id":"web-notes","display_name":"Notes","description":"unified notes","revision":"3","distribution":{"share_enabled":true,"market_enabled":false}}],"next_cursor":""}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/templates/tpl-notes":
			io.WriteString(w, `{"template_id":"tpl-notes","request_id":"del-1","revision":"4","cleanup_job_id":9,"status":"deleted"}`)
		default:
			t.Errorf("unexpected %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_test")

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"web", "list-template", "--q", "notes", "--json"}, `"template_id":"tpl-notes"`},
		{[]string{"web", "status-share-template", "web-notes", "--json"}, `"template_id":"tpl-notes"`},
		{[]string{"web", "delete-template", "tpl-notes", "--request-id", "del-1", "--expected-revision", "3", "--json"}, `"status":"deleted"`},
	} {
		var out, diag bytes.Buffer
		code := runCLI(context.Background(), tc.args, strings.NewReader(""), &out, &diag)
		if code != 0 || !strings.Contains(out.String(), tc.want) {
			t.Fatalf("args=%v code=%d out=%s diag=%s", tc.args, code, out.String(), diag.String())
		}
	}
}

func TestUnifiedInitCommandsSurfaceAPICodeOnSourceUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/template-catalog/tpl-nosrc/source" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		w.Header().Set("X-Request-Id", "req-nosrc-409")
		w.WriteHeader(http.StatusConflict)
		io.WriteString(w, `{"error":{"code":"TEMPLATE_SOURCE_UNAVAILABLE","message":"source unavailable"}}`)
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_test")

	for i, args := range [][]string{
		{"web", "init-template", "tpl-nosrc", "--dir"},
		{"web", "init-share-template", server.URL + "/templates/tpl-nosrc", "--dir"},
		{"web", "init-unified-template", "tpl-nosrc", "--channel", "share", "--dir"},
	} {
		target := filepath.Join(t.TempDir(), "target-"+string(rune('a'+i)))
		fullArgs := append(append([]string{}, args...), target, "--json")
		var out, diag bytes.Buffer
		code := runCLI(context.Background(), fullArgs, strings.NewReader(""), &out, &diag)
		if code != 1 {
			t.Fatalf("args=%v code=%d out=%s diag=%s", fullArgs, code, out.String(), diag.String())
		}
		for _, want := range []string{`"code":"WEB_TEMPLATE_FAILED"`, `"api_code":"TEMPLATE_SOURCE_UNAVAILABLE"`, `"request_id":"req-`, `TEMPLATE_SOURCE_UNAVAILABLE`} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("args=%v missing %s in out=%s", fullArgs, want, out.String())
			}
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatalf("args=%v created target dir %s", fullArgs, target)
		}
	}
}
