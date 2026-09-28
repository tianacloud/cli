package main

import (
	"bytes"
	"context"
	"fmt"
	"github.com/tianacloud/cli/internal/authclient"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppPreviewSavedAccountOutputFailureRetainsRequestIDs(t *testing.T) {
	for _, mode := range []string{"default", "specified"} {
		t.Run(mode, func(t *testing.T) {
			var ids []string
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ids = append(ids, r.Header.Get("X-Request-ID"))
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "whoami") {
					fmt.Fprint(w, `{"user":{"user_id":"usr_cli"},"sync_status":"complete"}`)
					return
				}
				fmt.Fprint(w, `{"id":"ins_fixture","engine":"sqlite","endpoint_id":"ep-7k3rbz6104qg3qk2z6de5z2vmh","connection":{"hostname":"ep-7k3rbz6104qg3qk2z6de5z2vmh.db.example.test"}}`)
			}))
			defer peer.Close()
			env := newTestEnv(t, peer.URL)

			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			defaultPath, err := authclient.DefaultCredentialPath()
			if err != nil {
				t.Fatal(err)
			}
			if mode == "specified" {
				saveTestCredential(t, peer.URL, env.credentialsPath, "usr_cli")
			} else {
				t.Setenv("TIANA_CREDENTIALS_FILE", "")
				saveTestCredential(t, peer.URL, defaultPath, "usr_cli")
			}
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "app.js"), []byte(`export function mount(){}`), 0600)
			os.WriteFile(filepath.Join(dir, "tiana.app.json"), []byte(`{"schema_version":1,"app_id":"fixture","name":"Fixture","rendering":"csr","routing":"hash","entry":"app.js","database_instance_id":"ins_fixture"}`), 0600)
			l, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := l.Addr().(*net.TCPAddr).Port
			l.Close()
			var diagnostics bytes.Buffer
			code := runCLI(context.Background(), []string{"app", "serve", "--dir", dir, "--port", fmt.Sprint(port)}, nil, failedReceiptWriter{}, &diagnostics)
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
