package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

func TestInstanceDeleteForceAndReceipt(t *testing.T) {
	for _, product := range []string{"sqlite", "git"} {
		for _, tc := range []struct {
			name, body   string
			status, want int
		}{
			{"accepted", `{"instance_id":"inst_cli","operation_id":"op-delete"}`, 202, 0},
			{"wrong instance", `{"instance_id":"inst_other","operation_id":"op-delete"}`, 202, 1},
			{"missing operation", `{"instance_id":"inst_cli"}`, 202, 1},
			{"wrong status", `{"instance_id":"inst_cli","operation_id":"op-delete"}`, 200, 1},
			{"denied", `{"error":{"code":"AUTHORIZATION_DENIED","message":"secret-peer-message"}}`, 403, 1},
			{"unavailable", `{"error":{"code":"UNAVAILABLE"}}`, 503, 1},
		} {
			t.Run(product+"/"+tc.name, func(t *testing.T) {
				deletes := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/api/v1/instances/inst_cli" {
						t.Errorf("unexpected path %s", r.URL.Path)
						http.NotFound(w, r)
						return
					}
					switch r.Method {
					case "GET":
						json.NewEncoder(w).Encode(authclient.Instance{ID: "inst_cli", DisplayName: "demo", Engine: product})
					case "DELETE":
						deletes++
						if r.Header.Get("Authorization") != "Bearer access-usr_cli" || r.Header.Get("Idempotency-Key") == "" {
							t.Error("missing authentication/request identity")
						}
						body, _ := io.ReadAll(r.Body)
						if len(body) != 0 {
							t.Error("delete should have no body")
						}
						w.WriteHeader(tc.status)
						fmt.Fprint(w, tc.body)
					default:
						t.Errorf("unexpected method %s", r.Method)
					}
				}))
				defer server.Close()
				env := newTestEnv(t, server.URL)
				saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
				var out, diag bytes.Buffer
				code := runCLI(context.Background(), []string{product, "delete", "inst_cli", "-f"}, strings.NewReader(""), &out, &diag)
				if code != tc.want || deletes != 1 || strings.Contains(diag.String(), "secret-peer-message") {
					t.Fatalf("code=%d deletes=%d out=%s diag=%s", code, deletes, &out, &diag)
				}
				if tc.want == 0 && (!strings.Contains(out.String(), "Deletion accepted") || !strings.Contains(out.String(), "op-delete")) {
					t.Fatalf("invalid receipt output: %s", &out)
				}
				if tc.want != 0 && out.Len() != 0 {
					t.Fatalf("false success: %s", &out)
				}
			})
		}
	}
}

func TestInstanceDeleteRefusesUnsafeTargets(t *testing.T) {
	for _, product := range []string{"sqlite", "git"} {
		for _, mode := range []string{"wrong-engine", "wrong-id", "empty-id", "duplicate", "missing", "wrong-name", "name", "pending", "nonterminal"} {
			t.Run(product+"/"+mode, func(t *testing.T) {
				deletes := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "DELETE" {
						deletes++
						if r.URL.Path != "/api/v1/instances/inst_cli" {
							t.Error("deleted unresolved target")
						}
						w.WriteHeader(202)
						fmt.Fprint(w, `{"instance_id":"inst_cli","operation_id":"op-delete"}`)
						return
					}
					if r.Method != "GET" {
						t.Errorf("unexpected %s", r.Method)
						return
					}
					item := authclient.Instance{ID: "inst_cli", Engine: product, DisplayName: "demo"}
					switch mode {
					case "wrong-engine":
						item.Engine = "other"
					case "wrong-id":
						item.ID = "inst_other"
					case "empty-id":
						item.ID = ""
					}
					if r.URL.Path == "/api/v1/instances/demo" {
						w.WriteHeader(400)
						fmt.Fprint(w, `{"error":{"code":"INVALID_INSTANCE_ID"}}`)
						return
					}
					if r.URL.Path == "/api/v1/instances" {
						if r.URL.Query().Get("display_name") != "demo" {
							t.Error("name filter missing")
						}
						items := []authclient.Instance{item}
						switch mode {
						case "duplicate":
							items = append(items, authclient.Instance{ID: "inst_other", Engine: product, DisplayName: "demo"})
						case "missing":
							items = nil
						case "wrong-name":
							items[0].DisplayName = "other"
						}
						json.NewEncoder(w).Encode(authclient.InstancePage{Items: items, Total: len(items), TotalPages: 1, Page: 1})
						return
					}
					json.NewEncoder(w).Encode(item)
				}))
				defer server.Close()
				env := newTestEnv(t, server.URL)
				saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
				var before []byte
				if mode == "pending" {
					s := authclient.NewFilePendingCommandStore(env.pendingPath)
					if err := s.Save(authclient.PendingCommand{Command: "db.create", Origin: server.URL, Args: []string{"create", "existing"}, IdempotencyKey: "original", CreatedAt: time.Now()}); err != nil {
						t.Fatal(err)
					}
					before, _ = os.ReadFile(env.pendingPath)
				}
				ref := "inst_cli"
				if mode == "name" || mode == "duplicate" || mode == "missing" || mode == "wrong-name" {
					ref = "demo"
				}
				args := []string{product, "delete", ref, "--force"}
				if mode == "nonterminal" {
					args = args[:3]
				}
				var out, diag bytes.Buffer
				code := runCLI(context.Background(), args, strings.NewReader("yes\n"), &out, &diag)
				if mode == "name" {
					if code != 0 || deletes != 1 {
						t.Fatalf("code=%d deletes=%d diag=%s", code, deletes, &diag)
					}
				} else if code == 0 || deletes != 0 {
					t.Fatalf("unsafe deletion code=%d deletes=%d diag=%s", code, deletes, &diag)
				}
				if before != nil {
					after, _ := os.ReadFile(env.pendingPath)
					if !bytes.Equal(before, after) {
						t.Fatal("pending changed")
					}
				}
			})
		}
	}
}

func TestInstanceDeleteArguments(t *testing.T) {
	for _, product := range []string{"sqlite", "git"} {
		for _, tail := range [][]string{{}, {"one", "two"}, {"one", "--branch", "main"}, {"one", "-f", "--force"}, {" one "}} {
			var out, diag bytes.Buffer
			if code := runCLI(context.Background(), append([]string{product, "delete"}, tail...), strings.NewReader(""), &out, &diag); code != 2 {
				t.Fatalf("args=%v code=%d diag=%s", tail, code, &diag)
			}
		}
		var out, diag bytes.Buffer
		if code := runCLI(context.Background(), []string{product, "delete", "--help"}, strings.NewReader(""), &out, &diag); code != 0 || !strings.Contains(out.String(), "--force") {
			t.Fatalf("help code=%d out=%s", code, &out)
		}
	}
}
