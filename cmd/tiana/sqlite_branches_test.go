package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/testutil/sqlitepeer"
)

func TestSQLiteBranchCommandsSelectChildEndpoint(t *testing.T) {
	child := "ep-1abcdefghjkmnpqrstvwxyz012"
	name := "开发 + %_ /&= "
	lists, details := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/instances/" + testInstanceID:
			io.WriteString(w, sqliteInstanceResponse())
		case "/api/v1/instances/" + testInstanceID + "/branches":
			lists++
			if r.URL.Query().Get("name") == name {
				if len(r.URL.Query()) != 1 {
					t.Errorf("query=%s", r.URL)
				}
			} else if r.URL.Query().Get("search") != "开发" || (r.URL.Query().Get("after") != "main" && r.URL.Query().Get("after") != "child") {
				t.Errorf("query=%s", r.URL)
			}
			next := "child"
			if r.URL.Query().Get("after") == "child" {
				_ = json.NewEncoder(w).Encode(authclient.BranchPage{})
				return
			}
			_ = json.NewEncoder(w).Encode(authclient.BranchPage{Items: []authclient.Branch{{ID: "child", Name: name, EndpointID: child}}, NextCursor: next})
		case "/api/v1/instances/" + testInstanceID + "/branches/child":
			details++
			io.WriteString(w, sqliteBranchResponse(testInstanceID, "child", name, child))
		default:
			t.Errorf("wrong branch request=%s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_branch")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"show", testInstanceID, "--branch", name, "--url"}, "https://" + child + ".db.example.test\n"},
		{[]string{"branch", "list", testInstanceID, "--after", "main", "--search", "开发"}, "BRANCH ID"},
	} {
		var out, diag bytes.Buffer
		if code := runSQLite(context.Background(), tc.args, strings.NewReader(""), &out, &diag); code != 0 || !strings.Contains(out.String(), tc.want) {
			t.Fatalf("args=%v code=%d diagnostics=%s", tc.args, code, &diag)
		}
	}
	if _, err := os.Stat(env.tokensPath); !os.IsNotExist(err) {
		t.Fatal("unexpected Token cache")
	}
	if lists != 3 || details != 1 {
		t.Fatalf("list=%d detail=%d", lists, details)
	}
}

func TestSQLiteShellBranchTokenSelection(t *testing.T) {
	for _, mode := range []string{"account", "explicit"} {
		t.Run(mode, func(t *testing.T) {
			child := "ep-1abcdefghjkmnpqrstvwxyz012"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/auth/transactions/whoami":
					io.WriteString(w, `{"user":{"user_id":"usr_branch","tenant_id":"tenant","email":"branch@example.test"}}`)
				case "/api/v1/instances/" + testInstanceID:
					io.WriteString(w, sqliteInstanceResponse())
				case "/api/v1/instances/" + testInstanceID + "/branches":
					if r.URL.Query().Get("name") != "development" {
						t.Errorf("query=%s", r.URL)
					}
					io.WriteString(w, `{"items":[{"branch_id":"child","name":"development"}]}`)
				case "/api/v1/instances/" + testInstanceID + "/branches/child":
					io.WriteString(w, sqliteBranchResponse(testInstanceID, "child", "development", child))
				default:
					t.Errorf("request=%s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_branch")
			t.Setenv("TIANA_TOKEN", "")
			os.Unsetenv("TIANA_TOKEN")
			sameValue := "tia_" + strings.Repeat("A", 43)
			os.WriteFile(env.tokensPath, []byte("broken legacy cache"), 0600)
			if mode == "explicit" {
				t.Setenv("TIANA_TOKEN", sameValue)
				os.WriteFile(env.tokensPath, []byte("corrupt"), 0600)
			}
			script := filepath.Join(t.TempDir(), "query.sql")
			if err := os.WriteFile(script, []byte("SELECT 1;"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, tail := range [][]string{{"-e", "SELECT 1"}, {"-f", script}, {}} {
				var out, diag bytes.Buffer
				config, dials := sqlitepeer.Gateway(t, func(io.Reader, io.Writer) { t.Error("SQL after refusal") }, true)
				args := append([]string{"sqlite", "shell", testInstanceID, "--branch", "development"}, tail...)
				code := runCLIWithSQL(context.Background(), args, strings.NewReader("SELECT 1;\n"), &out, &diag, func(ctx context.Context, o sqliteOptions) int {
					if o.branch != "development" {
						t.Fatalf("branch=%q", o.branch)
					}
					return executeSQLite(ctx, o, strings.NewReader("SELECT 1;\n"), &out, &diag, func(ctx context.Context, ref string, ni bool) (sqliteResolution, error) {
						return resolveSQLite(ctx, ref, o.branch, ni, &diag)
					}, &config)
				})
				if code != 3 || dials.Load() != 1 {
					t.Fatalf("code=%d dials=%d diag=%s", code, dials.Load(), &diag)
				}
			}

			if data, e := os.ReadFile(env.tokensPath); e != nil || (string(data) != "broken legacy cache" && string(data) != "corrupt") {
				t.Fatal("legacy cache modified")
			}

		})
	}
}

func TestSQLiteShowResolvesInstanceNameBeforeBranch(t *testing.T) {
	child := "ep-1abcdefghjkmnpqrstvwxyz012"
	for _, branch := range []string{"", "development"} {
		t.Run("branch="+branch, func(t *testing.T) {
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.Path)
				if r.Method != http.MethodGet {
					t.Errorf("unexpected method: %s", r.Method)
				}
				switch r.URL.Path {
				case "/api/v1/instances/sqlite-db":
					w.WriteHeader(http.StatusBadRequest)
					io.WriteString(w, `{"error":{"code":"INVALID_INSTANCE_ID"}}`)
				case "/api/v1/instances":
					if r.URL.Query().Get("display_name") != "sqlite-db" || r.URL.Query().Get("page") != "1" || r.URL.Query().Get("page_size") != "20" || len(r.URL.Query()) != 3 {
						t.Errorf("incorrect instance query: %s", r.URL)
					}
					io.WriteString(w, `{"items":[`+sqliteInstanceResponse()+`],"total":1,"total_pages":1,"page":1,"page_size":20}`)
				case "/api/v1/instances/" + testInstanceID + "/branches":
					if r.URL.Query().Get("name") != "development" || len(r.URL.Query()) != 1 {
						t.Errorf("incorrect branch query: %s", r.URL)
					}
					io.WriteString(w, `{"items":[{"branch_id":"child","name":"development"}]}`)
				case "/api/v1/instances/" + testInstanceID + "/branches/main":
					io.WriteString(w, sqliteBranchResponse(testInstanceID, "main", "main", testEndpointID))
				case "/api/v1/instances/" + testInstanceID + "/branches/child":
					io.WriteString(w, sqliteBranchResponse(testInstanceID, "child", "development", child))
				default:
					t.Errorf("unexpected request: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_branch")
			args := []string{"show", "sqlite-db", "--url"}
			endpoint, count := testEndpointID, 3
			if branch != "" {
				args = append(args, "--branch", branch)
				endpoint, count = child, 4
			}
			var out, diag bytes.Buffer
			code := runSQLite(context.Background(), args, strings.NewReader(""), &out, &diag)
			if code != 0 || out.String() != "https://"+endpoint+".db.example.test\n" || len(requests) != count {
				t.Fatalf("code=%d requests=%v out=%s diagnostics=%s", code, requests, &out, &diag)
			}
		})
	}
}
