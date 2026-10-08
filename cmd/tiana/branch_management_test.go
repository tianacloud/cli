package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tianacloud/cli/internal/authclient"
)

func TestBranchMutations(t *testing.T) {
	for _, action := range []string{"create", "delete"} {
		for _, mode := range []string{"accepted", "parent", "by-id", "wrong-engine", "root", "protected", "wrong-instance", "wrong-branch", "missing-id", "ambiguous", "pending", "nonterminal", "unavailable", "bad-receipt", "wrong-status"} {
			if action == "create" && (mode == "root" || mode == "protected" || mode == "by-id" || mode == "nonterminal") {
				continue
			}
			if action == "delete" && mode == "parent" {
				continue
			}
			t.Run(action+"/"+mode, func(t *testing.T) {
				writes := 0
				branchID := "child"
				if action == "create" && mode != "parent" {
					branchID = "main"
				}
				if mode == "root" {
					branchID = "main"
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					base := "/api/v1/instances/inst_cli"
					if r.Method != "GET" {
						writes++
						wantMethod, wantPath := "DELETE", base+"/branches/"+branchID
						if action == "create" {
							wantMethod = "POST"
							wantPath += "/children"
						}
						if r.Method != wantMethod || r.URL.Path != wantPath || r.Header.Get("Idempotency-Key") == "" || r.Header.Get("Authorization") != "Bearer access-usr_cli" {
							t.Errorf("bad mutation %s %s", r.Method, r.URL)
						}
						body, _ := io.ReadAll(r.Body)
						if action == "create" {
							var request map[string]any
							if json.Unmarshal(body, &request) != nil || request["name"] != "preview" || request["ttl_seconds"] != nil || len(request) != 2 {
								t.Errorf("bad create body %s", body)
							}
						} else if len(body) != 0 {
							t.Error("delete body")
						}
						if mode == "unavailable" {
							w.WriteHeader(503)
							fmt.Fprint(w, `{"error":{"code":"UNAVAILABLE","message":"PRIVATE_SERVER_MESSAGE"}}`)
							return
						}
						status := 202
						if mode == "wrong-status" {
							status = 200
						}
						w.WriteHeader(status)
						id := "inst_cli"
						if mode == "bad-receipt" {
							id = "other"
						}
						fmt.Fprintf(w, `{"instance_id":%q,"operation_id":"op-branch"}`, id)
						return
					}
					switch r.URL.Path {
					case base:
						engine := "sqlite"
						if mode == "wrong-engine" {
							engine = "git"
						}
						json.NewEncoder(w).Encode(authclient.Instance{ID: "inst_cli", Engine: engine, DisplayName: "demo"})
					case base + "/branches":
						if r.URL.Query().Get("name") != "preview" {
							t.Errorf("not exact-name query: %s", r.URL)
						}
						items := []authclient.Branch{{ID: branchID, Name: "preview"}}
						if mode == "ambiguous" {
							items = append(items, items[0])
						}
						if mode == "missing-id" {
							items[0].ID = ""
						}
						json.NewEncoder(w).Encode(authclient.BranchPage{Items: items})
					case base + "/branches/" + branchID:
						id, bid := "inst_cli", branchID
						if mode == "wrong-instance" {
							id = "other"
						}
						if mode == "wrong-branch" {
							bid = "other"
						}
						fmt.Fprintf(w, `{"instance_id":%q,"branch":{"branch_id":%q,"name":"preview","root":%t,"protected":%t}}`, id, bid, bid == "main", mode == "protected")
					default:
						t.Errorf("unexpected read %s", r.URL)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				env := newTestEnv(t, server.URL)
				saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
				if mode == "pending" {
					if err := authclient.NewFilePendingCommandStore(env.pendingPath).Save(authclient.PendingCommand{Command: "db.create", Origin: server.URL, IdempotencyKey: "pending"}); err != nil {
						t.Fatal(err)
					}
				}
				args := []string{"sqlite", "branch", action, "inst_cli", "preview"}
				if action == "create" && (mode == "parent" || mode == "ambiguous" || mode == "missing-id") {
					args = append(args, "--parent", "preview")
					branchID = "child"
				}
				if action == "delete" && mode != "nonterminal" {
					args = append(args, "-f")
				}
				if mode == "by-id" {
					args = []string{"sqlite", "branch", "delete", "inst_cli", "child", "--by-id", "-f"}
				}
				var out, diag bytes.Buffer
				code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diag)
				success := mode == "accepted" || mode == "parent" || mode == "by-id"
				sent := success || mode == "unavailable" || mode == "bad-receipt" || mode == "wrong-status"
				expectedWrites := 0
				if sent {
					expectedWrites = 1
				}
				if writes != expectedWrites || (code == 0) != success {
					t.Fatalf("code=%d writes=%d want=%d out=%s diag=%s", code, writes, expectedWrites, &out, &diag)
				}
				if success && !strings.Contains(out.String(), "op-branch") {
					t.Fatalf("missing receipt %s", &out)
				}
				if !success && out.Len() != 0 {
					t.Fatalf("false success %s", &out)
				}
				if strings.Contains(diag.String(), "PRIVATE_") {
					t.Fatal("leaked server message")
				}
			})
		}
	}
}

func TestBranchCommandArguments(t *testing.T) {
	for _, args := range [][]string{{"sqlite", "branches", "list", "instance"}, {"sqlite", "branch", "create", "instance"}, {"sqlite", "branch", "delete", "instance"}, {"sqlite", "branch", "create", "instance", "name", "--parent="}, {"sqlite", "branch", "delete", "instance", "../other", "--by-id", "-f"}} {
		var out, diag bytes.Buffer
		if code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diag); code != 2 {
			t.Errorf("args=%v code=%d diag=%s", args, code, &diag)
		}
	}
}
