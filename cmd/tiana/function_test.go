package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runFunction(args ...string) (int, string, string) {
	var out, diagnostics bytes.Buffer
	code := runCLI(context.Background(), append([]string{"function"}, args...), strings.NewReader(""), &out, &diagnostics)
	return code, out.String(), diagnostics.String()
}

func TestFunctionHelpAndArguments(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"create", "--help"}, {"source", "save", "--help"}, {"parameters", "set", "--help"}, {"cors", "get", "--help"}, {"publish", "--help"}} {
		if code, _, diag := runFunction(args...); code != 0 {
			t.Fatalf("%v code=%d %s", args, code, diag)
		}
	}
	for _, args := range [][]string{{"create"}, {"create", "a", "b"}, {"source", "read", "fn-test"}, {"source", "save", "fn-test"}, {"publish"}, {"list", "extra"}, {"list", "--page", "0"}} {
		if code, _, diag := runFunction(args...); code != 2 {
			t.Fatalf("%v code=%d %s", args, code, diag)
		}
	}
}

func TestFunctionListReadsRequestedPage(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/instances" || r.URL.Query().Get("engine") != "function" || r.URL.Query().Get("page") != "2" {
			t.Errorf("unexpected request %s", r.URL)
		}
		ioBody := `{"items":[{"id":"fn-demo","engine":"function","display_name":"backend"}],"page":2,"page_size":20,"total":45,"total_pages":3}`
		w.Write([]byte(ioBody))
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_function")
	code, out, diag := runFunction("list", "--page", "2", "--json")
	if code != 0 || calls != 1 || !strings.Contains(out, `"page":2`) {
		t.Fatalf("code=%d calls=%d out=%s diag=%s", code, calls, out, diag)
	}
}

func TestFunctionLogsReadsTextResponse(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/instances/fn-demo":
					w.Write([]byte(`{"id":"fn-demo","engine":"function"}`))
				case "/api/v1/functions/fn-demo/builds/42/logs":
					if r.Header.Get("Authorization") == "" || r.Header.Get("X-Request-ID") == "" {
						t.Error("missing account credential or request identity")
					}
					w.Header().Set("Content-Type", "text/plain; charset=utf-8")
					w.Header().Set("X-Tiana-Dropped-Log-Bytes", "23")
					w.WriteHeader(status)
					w.Write([]byte("编译完成\nartifact uploaded\n"))
				default:
					t.Errorf("unexpected request %s", r.URL)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			caFile := filepath.Join(t.TempDir(), "ca.pem")
			if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TIANA_CA_FILE", caFile)
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_function")
			code, out, diag := runFunction("logs", "fn-demo", "--build", "42", "--json")
			if status != http.StatusOK {
				if code == 0 {
					t.Fatalf("failed log returned success: %s", out)
				}
				return
			}
			var result struct {
				Data struct {
					Text         string `json:"text"`
					DroppedBytes string `json:"dropped_bytes"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(out), &result); code != 0 || err != nil || result.Data.Text != "编译完成\nartifact uploaded\n" || result.Data.DroppedBytes != "23" {
				t.Fatalf("code=%d out=%s diag=%s", code, out, diag)
			}
		})
	}
}

func TestFunctionParametersReplacement(t *testing.T) {
	file := filepath.Join(t.TempDir(), "parameters.json")
	if err := os.WriteFile(file, []byte(`{"MESSAGE":"hello","SQLITE_ENDPOINT":"db.example.test"}`), 0600); err != nil {
		t.Fatal(err)
	}
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/instances/fn-demo":
			w.Write([]byte(`{"id":"fn-demo","engine":"function"}`))
		case "PUT /api/v1/functions/fn-demo/parameters":
			puts++
			var body struct {
				Parameters map[string]string `json:"parameters"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Parameters["MESSAGE"] != "hello" {
				t.Errorf("wrong replacement: %+v %v", body, err)
			}
			w.Write([]byte(`{"parameters":{"MESSAGE":"hello"},"application":"applied"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_function")
	code, out, diag := runFunction("parameters", "set", "fn-demo", "--file", file, "--json")
	if code != 0 || puts != 1 || !strings.Contains(out, `"application":"applied"`) {
		t.Fatalf("code=%d puts=%d out=%s diag=%s", code, puts, out, diag)
	}
}

func TestFunctionPublishAndRecoveryIdentity(t *testing.T) {
	for _, state := range []string{"published", "failed", "queued", "same-commit"} {
		t.Run(state, func(t *testing.T) {
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "GET /api/v1/instances/fn-demo":
					w.Write([]byte(`{"id":"fn-demo","engine":"function"}`))
				case "GET /api/v1/functions/fn-demo/release":
					w.Write([]byte(`{"published":{"build_commit":"commit-main"}}`))
				case "POST /api/v1/functions/fn-demo/builds":
					posts++
					var body map[string]string
					json.NewDecoder(r.Body).Decode(&body)
					if body["request_id"] != "req-original" || body["source_commit"] != "commit-main" {
						t.Errorf("lost identity: %v", body)
					}
					w.WriteHeader(202)
					json.NewEncoder(w).Encode(map[string]string{"id": "42", "sequence": "7", "status": state, "error_code": "BUILD_FAILED"})
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_function")
			args := []string{"publish", "fn-demo", "--commit", "commit-main", "--json"}
			if state != "same-commit" {
				args = append(args, "--request-id", "req-original")
			}
			if state != "queued" {
				args = append(args, "--wait")
			}
			code, out, diag := runFunction(args...)
			if (code != 0) != (state == "failed") || posts != map[bool]int{true: 0, false: 1}[state == "same-commit"] {
				t.Fatalf("code=%d posts=%d out=%s diag=%s", code, posts, out, diag)
			}
			if state == "queued" && !strings.Contains(out, `"status":"accepted"`) {
				t.Fatal(out)
			}
			if state == "same-commit" && !strings.Contains(out, `"already_published":true`) {
				t.Fatal(out)
			}
		})
	}
}
