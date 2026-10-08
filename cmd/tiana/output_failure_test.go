package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagementOutputFailureRetainsRequestIDs(t *testing.T) {
	for _, engine := range []string{"sqlite", "git"} {
		for _, mode := range []string{"list", "empty", "show", "url", "branches"} {
			if engine == "git" && mode == "branches" {
				continue
			}
			t.Run(engine+"/"+mode, func(t *testing.T) {
				var ids []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					ids = append(ids, r.Header.Get("X-Request-ID"))
					instance := fmt.Sprintf(`{"id":"inst_cli","engine":%q,"product_state":"ACTIVE","endpoint_id":"ep-00000000000000000000000000","connection":{"hostname":"ep-00000000000000000000000000.db.example.test","url":"https://ep-00000000000000000000000000.db.example.test"}}`, engine)
					switch r.URL.Path {
					case "/api/v1/instances":
						if mode == "empty" {
							fmt.Fprint(w, `{"items":[],"total_pages":0}`)
						} else {
							fmt.Fprintf(w, `{"items":[%s],"total_pages":1}`, instance)
						}
					case "/api/v1/instances/inst_cli":
						fmt.Fprint(w, instance)
					case "/api/v1/instances/inst_cli/branches/main":
						fmt.Fprint(w, `{"instance_id":"inst_cli","branch":{"branch_id":"main","name":"main","root":true},"connection":{"url":"https://ep-00000000000000000000000000.db.example.test"}}`)
					case "/api/v1/instances/inst_cli/branches":
						fmt.Fprint(w, `{"items":[{"branch_id":"main","name":"main"}]}`)
					default:
						t.Errorf("unexpected %s", r.URL)
					}
				}))
				defer server.Close()
				env := newTestEnv(t, server.URL)
				saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
				t.Setenv("TIANA_DIAGNOSTICS", "0")
				args := []string{engine, "list"}
				if mode == "show" || mode == "url" {
					args = []string{engine, "show", "inst_cli"}
				}
				if mode == "url" {
					args = append(args, "--url")
				}
				if mode == "branches" {
					args = []string{engine, "branch", "list", "inst_cli"}
				}
				var diag bytes.Buffer
				code := runCLI(context.Background(), args, strings.NewReader(""), failedReceiptWriter{}, &diag)
				if code != 1 || len(ids) == 0 {
					t.Fatalf("code=%d requests=%d diag=%s", code, len(ids), &diag)
				}
				for _, id := range ids {
					if id == "" || !strings.Contains(diag.String(), id) {
						t.Fatalf("missing original ID: %s", &diag)
					}
				}
			})
		}
	}
}

func TestSessionOutputFailureRetainsRequestIDs(t *testing.T) {
	for _, command := range []string{"logout", "login-start", "legacy-login"} {
		t.Run(command, func(t *testing.T) {
			var ids []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ids = append(ids, r.Header.Get("X-Request-ID"))
				if command == "login-start" || command == "legacy-login" {
					fmt.Fprint(w, `{"transaction_id":"fixture","client_secret":"fixture-secret","user_code":"TEST","verification_uri_complete":"https://console.example.test/approve","expires_in":600,"poll_interval":1}`)
				} else {
					fmt.Fprint(w, `{}`)
				}
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
			t.Setenv("TIANA_DIAGNOSTICS", "0")
			t.Setenv("TIANA_PENDING_LOGIN_FILE", filepath.Join(t.TempDir(), "login.json"))
			args := []string{"logout"}
			if command == "login-start" {
				args = []string{"login", "--start", "--no-open"}
			}
			if command == "legacy-login" {
				args = []string{"login"}
			}
			var diag bytes.Buffer
			code := runCLI(context.Background(), args, strings.NewReader(""), failedReceiptWriter{}, &diag)
			if code != 1 || len(ids) != 1 {
				t.Fatalf("code=%d requests=%d diag=%s", code, len(ids), &diag)
			}
			for _, id := range ids {
				if id == "" || !strings.Contains(diag.String(), id) {
					t.Fatal("request ID lost")
				}
			}
			if command == "logout" {
				if _, err := os.Stat(env.credentialsPath); err == nil {
					t.Fatal("logout did not delete credentials")
				}
			}
		})
	}
}
