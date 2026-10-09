package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/tianacloud/cli/internal/apppublish"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func decodeManagementJSON(t *testing.T, out *bytes.Buffer) map[string]any {
	t.Helper()
	var got map[string]any
	decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
	if err := decoder.Decode(&got); err != nil {
		t.Fatalf("expected management JSON, got decoding error %v", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatal("stdout contained multiple results or human text")
	}
	return got
}

func TestManagementJSONReadCommands(t *testing.T) {
	for _, tc := range []struct {
		name, engine string
		args         []string
	}{
		{"sqlite-list", "sqlite", []string{"sqlite", "list", "--json"}},
		{"git-list", "git", []string{"git", "list", "--json"}},
		{"sqlite-show", "sqlite", []string{"sqlite", "show", "inst_cli", "--json"}},
		{"git-show", "git", []string{"git", "show", "inst_cli", "--json"}},
		{"branch-list", "sqlite", []string{"sqlite", "branch", "list", "inst_cli", "--json"}},
		{"status", "sqlite", []string{"status", "--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("read command issued a write")
					w.WriteHeader(500)
					return
				}
				switch r.URL.Path {
				case "/api/v1/auth/transactions/whoami":
					io.WriteString(w, `{"user":{"user_id":"owner","tenant_id":"tenant","email":"owner@example.test"},"access_token":"synthetic-hidden"}`)
				case "/api/v1/instances":
					calls++
					page := r.URL.Query().Get("page")
					fmt.Fprintf(w, `{"items":[{"id":"inst-%s","engine":"%s","display_name":"example","product_state":"ACTIVE","current_job_id":9007199254740993,"product_revision":"18446744073709551615","token":"synthetic-hidden"}],"total_pages":2}`, page, tc.engine)
				case "/api/v1/instances/inst_cli":
					fmt.Fprintf(w, `{"id":"inst_cli","engine":"%s","display_name":"example","product_state":"ACTIVE","current_job_id":9007199254740993,"product_revision":"18446744073709551615","token":"synthetic-hidden"}`, tc.engine)
				case "/api/v1/instances/inst_cli/branches/main":
					io.WriteString(w, `{"instance_id":"inst_cli","branch":{"branch_id":"main","name":"production","root":true}}`)
				case "/api/v1/instances/inst_cli/branches":
					calls++
					if r.URL.Query().Get("after") == "" {
						io.WriteString(w, `{"items":[{"branch_id":"main","name":"production","root":true}],"next_cursor":"next"}`)
					} else {
						io.WriteString(w, `{"items":[{"branch_id":"child","name":"preview"}],"next_cursor":""}`)
					}
				case "/api/v1/usage":
					io.WriteString(w, strings.ReplaceAll(testQuotaResponse, `"storage_used":"456"`, `"storage_used":"18446744073709551615"`))
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "owner")
			var out, diag bytes.Buffer
			code := runCLI(t.Context(), tc.args, strings.NewReader(""), &out, &diag)
			if code != 0 {
				t.Fatalf("JSON read failed: code=%d diagnostics=%s", code, &diag)
			}
			got := decodeManagementJSON(t, &out)
			if got["status"] != "succeeded" || got["error"] != nil {
				t.Fatalf("wrong envelope: %v", got["status"])
			}
			data := got["data"].(map[string]any)
			switch tc.name {
			case "sqlite-list", "git-list", "branch-list":
				if len(data["items"].([]any)) != 2 || calls != 2 {
					t.Fatalf("pagination incomplete: calls=%d", calls)
				}
			case "sqlite-show", "git-show":
				if data["current_job_id"] != "9007199254740993" || data["product_revision"] != "18446744073709551615" {
					t.Fatal("instance integers lost precision")
				}
			case "status":
				if data["logged_in"] != true || data["quota"].(map[string]any)["storage_used"] != "18446744073709551615" {
					t.Fatal("account/quota precision lost")
				}
			}
			if strings.Contains(out.String()+diag.String(), "synthetic-hidden") {
				t.Fatal("unexpected credential/error field leaked")
			}
		})
	}
}

func TestManagementJSONFailureHasNoPartialSuccess(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
		exit int
	}{
		{"unsigned", []string{"sqlite", "list", "--json"}, "AUTH_REQUIRED", 1},
		{"usage", []string{"sqlite", "show", "--json"}, "INVALID_INPUT", 2},
		{"early-unknown-option", []string{"sqlite", "list", "--bad-option", "--json"}, "INVALID_INPUT", 2},
		{"late-page", []string{"sqlite", "list", "--json"}, "MANAGEMENT_FAILED", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if tc.name != "late-page" {
					t.Error("local failure reached network")
				}
				if r.URL.Query().Get("page") == "1" {
					io.WriteString(w, `{"items":[{"id":"inst-1","engine":"sqlite"}],"total_pages":2}`)
				} else {
					w.WriteHeader(500)
					io.WriteString(w, `{"error":{"message":"synthetic-hidden"}}`)
				}
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			if tc.name == "late-page" {
				saveTestCredential(t, server.URL, env.credentialsPath, "owner")
			}
			var out, diag bytes.Buffer
			code := runCLI(t.Context(), tc.args, strings.NewReader(""), &out, &diag)
			got := decodeManagementJSON(t, &out)
			if code != tc.exit || got["status"] != "failed" || got["error"].(map[string]any)["code"] != tc.want {
				t.Fatalf("wrong failure: code=%d result=%v", code, got["status"])
			}
			if got["data"] != nil {
				t.Fatal("failed listing exposed a partial successful list")
			}
			if tc.name == "late-page" && calls != 2 {
				t.Fatal("unexpected page replay")
			}
			if strings.Contains(out.String()+diag.String(), "synthetic-hidden") {
				t.Fatal("raw peer error leaked")
			}
		})
	}
}

func TestManagementJSONMutationReceipts(t *testing.T) {
	for _, tc := range []struct {
		name, engine, kind, branch string
		args                       []string
		wait                       bool
	}{
		{"sqlite-delete", "sqlite", "DELETE_INSTANCE", "", []string{"sqlite", "delete", "inst_cli", "--force", "--json"}, false},
		{"git-delete-wait", "git", "DELETE_INSTANCE", "", []string{"git", "delete", "inst_cli", "--force", "--wait", "--json"}, true},
		{"branch-create", "sqlite", "CREATE_BRANCH", "child", []string{"sqlite", "branch", "create", "inst_cli", "preview", "--json"}, false},
		{"branch-create-wait", "sqlite", "CREATE_BRANCH", "child", []string{"sqlite", "branch", "create", "inst_cli", "preview", "--wait", "--json"}, true},
		{"branch-delete", "sqlite", "DELETE_BRANCH", "child", []string{"sqlite", "branch", "delete", "inst_cli", "child", "--by-id", "--force", "--json"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" || r.Method == "DELETE" {
					writes++
					w.WriteHeader(202)
					io.WriteString(w, `{"instance_id":"inst_cli","operation_id":"9007199254740993"}`)
					return
				}
				switch r.URL.Path {
				case "/api/v1/instances/inst_cli":
					fmt.Fprintf(w, `{"id":"inst_cli","engine":"%s","product_state":"ACTIVE"}`, tc.engine)
				case "/api/v1/instances/inst_cli/branches/main":
					io.WriteString(w, `{"instance_id":"inst_cli","branch":{"branch_id":"main","name":"production","root":true}}`)
				case "/api/v1/instances/inst_cli/branches/child":
					io.WriteString(w, `{"instance_id":"inst_cli","branch":{"branch_id":"child","name":"preview","root":false}}`)
				case "/api/v1/instances/inst_cli/operations/9007199254740993":
					fmt.Fprintf(w, `{"instance_id":"inst_cli","operation_id":"9007199254740993","kind":"%s","parent_branch_id":"main","branch_id":"%s","state":"success"}`, tc.kind, tc.branch)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "owner")
			var out, diag bytes.Buffer
			code := runCLI(t.Context(), tc.args, strings.NewReader(""), &out, &diag)
			if code != 0 {
				t.Fatalf("mutation failed code=%d diagnostics=%s", code, &diag)
			}
			got := decodeManagementJSON(t, &out)
			want := "accepted"
			if tc.wait {
				want = "succeeded"
			}
			data := got["data"].(map[string]any)
			if got["status"] != want || data["instance_id"] != "inst_cli" || data["operation_id"] != "9007199254740993" || writes != 1 {
				t.Fatalf("receipt changed or replayed: status=%v writes=%d", got["status"], writes)
			}
		})
	}
}

func TestVersionAndLogoutJSON(t *testing.T) {
	for _, args := range [][]string{{"version", "--json"}, {"logout", "--json"}} {
		t.Run(args[0], func(t *testing.T) {
			newTestEnv(t, "http://127.0.0.1:1")
			var out, diag bytes.Buffer
			code := runCLI(t.Context(), args, strings.NewReader(""), &out, &diag)
			if code != 0 {
				t.Fatalf("command failed: %d", code)
			}
			got := decodeManagementJSON(t, &out)
			if got["status"] != "succeeded" {
				t.Fatal("result was not successful")
			}
			data := got["data"].(map[string]any)
			if args[0] == "version" {
				if data["version"] != version || data["helper_contract"] != float64(3) {
					t.Fatal("version result incomplete")
				}
			} else if data["logged_in"] != false {
				t.Fatal("logout did not report signed-out state")
			}
		})
	}
}

func TestSQLJSONAliasPreservesSQLResultContract(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"sqlite", "shell", "inst_cli", "-e", "SELECT 1", "--json"}, 0},
		{[]string{"sqlite", "shell", "inst_cli", "-e", "SELECT 1", "--json", "--format", "table"}, 2},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			newTestEnv(t, "http://127.0.0.1:1")
			var out, diag bytes.Buffer
			code := runCLIWithSQL(t.Context(), tc.args, strings.NewReader(""), &out, &diag, func(ctx context.Context, o sqliteOptions) int {
				if tc.code != 0 {
					t.Fatal("conflicting SQL format reached executor")
				}
				if o.format != "json" || o.sql != "SELECT 1" {
					t.Fatalf("SQL alias normalization failed: format=%s", o.format)
				}
				io.WriteString(&out, `{"columns":["ready"],"rows":[["1"]]}`)
				return 0
			})
			if code != tc.code {
				t.Fatalf("exit=%d", code)
			}
			if tc.code == 0 && strings.Contains(out.String(), `"status"`) {
				t.Fatal("management envelope mixed with SQL results")
			}
		})
	}
}

func TestJSONMutationPreservesUnknownOutcomeWithoutReplay(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			calls++
			w.WriteHeader(500)
			io.WriteString(w, `{"error":{"message":"synthetic-hidden"}}`)
			return
		}
		io.WriteString(w, `{"id":"inst_cli","engine":"sqlite","product_state":"ACTIVE"}`)
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "owner")
	var out, diag bytes.Buffer
	code := runCLI(t.Context(), []string{"sqlite", "delete", "inst_cli", "--force", "--json"}, strings.NewReader(""), &out, &diag)
	got := decodeManagementJSON(t, &out)
	if code != 4 || got["status"] != "unknown" || calls != 1 {
		t.Fatalf("unknown mutation was replayed/mislabeled: code=%d calls=%d", code, calls)
	}
	if got["data"].(map[string]any)["instance_id"] != "inst_cli" {
		t.Fatal("original target lost")
	}
}

func TestJSONCreateDoesNotStartBrowserLogin(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(503) }))
	defer server.Close()
	newTestEnv(t, server.URL)
	var out, diag bytes.Buffer
	code := runCLI(t.Context(), []string{"sqlite", "create", "example", "--json"}, strings.NewReader(""), &out, &diag)
	if code == 0 || calls != 0 {
		t.Fatalf("JSON create initiated authentication: code=%d calls=%d", code, calls)
	}
	decodeManagementJSON(t, &out)
}

func TestJSONWaitDistinguishesFailedFromUnknown(t *testing.T) {
	for _, tc := range []struct {
		mode, status string
		code         int
	}{{"failed", "failed", 1}, {"missing", "unknown", 4}} {
		t.Run(tc.mode, func(t *testing.T) {
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "DELETE":
					writes++
					w.WriteHeader(202)
					io.WriteString(w, `{"instance_id":"inst_cli","operation_id":"17"}`)
				case r.URL.Path == "/api/v1/instances/inst_cli":
					io.WriteString(w, `{"id":"inst_cli","engine":"sqlite","product_state":"ACTIVE"}`)
				case r.URL.Path == "/api/v1/instances/inst_cli/operations/17":
					if tc.mode == "missing" {
						w.WriteHeader(404)
						io.WriteString(w, `{"error":{"code":"NOT_FOUND"}}`)
					} else {
						io.WriteString(w, `{"instance_id":"inst_cli","operation_id":"17","kind":"DELETE_INSTANCE","state":"failed"}`)
					}
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "owner")
			var out, diag bytes.Buffer
			code := runCLI(t.Context(), []string{"sqlite", "delete", "inst_cli", "--force", "--wait", "--json"}, strings.NewReader(""), &out, &diag)
			got := decodeManagementJSON(t, &out)
			if code != tc.code || got["status"] != tc.status || got["data"].(map[string]any)["operation_id"] != "17" || writes != 1 {
				t.Fatalf("observation lost fact/receipt: status=%v exit=%d writes=%d", got["status"], code, writes)
			}
		})
	}
}

func TestJSONFalseAndLiteralOperandKeepPlainOutput(t *testing.T) {
	for _, args := range [][]string{{"version", "--json=0"}, {"version", "--json=False"}} {
		newTestEnv(t, "http://127.0.0.1:1")
		var out, diag bytes.Buffer
		if code := runCLI(t.Context(), args, strings.NewReader(""), &out, &diag); code != 0 || !strings.HasPrefix(out.String(), "tiana ") {
			t.Fatalf("explicit false changed output: code=%d", code)
		}
	}
	newTestEnv(t, "http://127.0.0.1:1")
	var out, diag bytes.Buffer
	if code := runCLI(t.Context(), []string{"version", "--", "--json"}, strings.NewReader(""), &out, &diag); code != 2 || out.Len() != 0 {
		t.Fatal("literal operand was treated as an output flag")
	}
}

func TestPlainAppFailureEscapesLocalDiagnosticControls(t *testing.T) {
	var out, diag bytes.Buffer
	code := writeAppResult(apppublish.Failure(&apppublish.Error{Code: "LOCAL_STATE_UNAVAILABLE", Message: "state path /example/\x1b[2Jfile", NextAction: "repair\rpath", ExitCode: 1}), false, &out, &diag)
	if code != 1 || strings.ContainsAny(diag.String(), "\x1b\r") || !strings.Contains(diag.String(), "file") {
		t.Fatal("local path controls reached terminal diagnostics")
	}
}

func TestJSONCreateReportsLocalStateCause(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	blocked := filepath.Join(filepath.Dir(env.pendingPath), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TIANA_PENDING_COMMAND_FILE", filepath.Join(blocked, "pending.json"))
	var out, diag bytes.Buffer
	code := runCLI(t.Context(), []string{"sqlite", "create", "example", "--json"}, strings.NewReader(""), &out, &diag)
	got := decodeManagementJSON(t, &out)
	failure := got["error"].(map[string]any)
	if code != 1 || failure["code"] != "LOCAL_STATE_UNAVAILABLE" || !strings.Contains(failure["message"].(string), blocked) || calls != 0 {
		t.Fatalf("local cause hidden or request sent: code=%d calls=%d", code, calls)
	}
}
