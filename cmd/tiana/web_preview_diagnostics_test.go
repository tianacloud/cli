package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebPreviewSavedAccountOutputFailureRetainsRequestIDs(t *testing.T) {
	for _, mode := range []string{"default"} {
		t.Run(mode, func(t *testing.T) {
			var ids []string
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ids = append(ids, r.Header.Get("X-Request-ID"))
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/v1/web-projects/fixture" {
					fmt.Fprint(w, `{"id":"fixture","owner_id":"usr_cli","tenant_id":"","instance_id":"web-fixture","project_revision":1,"name":"Fixture","entry":"app.js","database_instance_id":"ins_fixture"}`)
					return
				}
				if strings.HasSuffix(r.URL.Path, "whoami") {
					fmt.Fprint(w, `{"user":{"user_id":"usr_cli"},"sync_status":"complete"}`)
					return
				}
				fmt.Fprint(w, `{"id":"ins_fixture","engine":"sqlite","endpoint_id":"ep-7k3rbz6104qg3qk2z6de5z2vmh","connection":{"hostname":"ep-7k3rbz6104qg3qk2z6de5z2vmh.db.example.test"}}`)
			}))
			defer peer.Close()
			env := newTestEnv(t, peer.URL)
			saveTestCredential(t, peer.URL, env.credentialsPath, "usr_cli")
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "app.js"), []byte(`export function mount(){}`), 0600)
			l, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := l.Addr().(*net.TCPAddr).Port
			l.Close()
			var diagnostics bytes.Buffer
			code := runCLI(context.Background(), []string{"web", "serve", "fixture", "--dir", dir, "--port", fmt.Sprint(port)}, nil, failedReceiptWriter{}, &diagnostics)
			if code != 1 || len(ids) < 3 {
				t.Fatalf("code=%d requests=%d", code, len(ids))
			}
			for _, id := range ids {
				if id == "" || !strings.Contains(diagnostics.String(), id) {
					t.Errorf("original request ID missing: %q", id)
				}
			}

		})
	}
}
