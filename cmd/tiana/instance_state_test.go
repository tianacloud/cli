package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Literal wire fixtures exercise JSON consumption and the actual list/show
// commands. Product state must not hide deletion, or mistake old operation IDs
// and stale runtime data for an active deletion.
func TestDeletionStateListAndShow(t *testing.T) {
	for _, product := range []string{"sqlite", "git"} {
		for _, tc := range []struct{ name, fields, want string }{
			{"pending", `"product_state":"ACTIVE","deletion_pending":true`, "DELETING"},
			{"lifecycle", `"product_state":"ACTIVE","lifecycle_state":"DELETING"`, "DELETING"},
			{"deleted", `"product_state":"DELETED","deletion_pending":true,"lifecycle_state":"DELETING"`, "DELETED"},
			{"runtime deleted", `"product_state":"ACTIVE","deletion_pending":true,"lifecycle_state":"DELETED"`, "DELETED"},
			{"stale pending", `"product_state":"ACTIVE","deletion_pending":true,"runtime_status_stale":true`, "DELETING"},
			{"stale runtime", `"product_state":"ACTIVE","runtime_status_stale":true,"lifecycle_state":"DELETING"`, "ACTIVE"},
			{"stale deleted", `"product_state":"ACTIVE","deletion_pending":true,"runtime_status_stale":true,"lifecycle_state":"DELETED"`, "DELETING"},
			{"failed operation", `"product_state":"ACTIVE","deletion_pending":false,"deletion_operation_id":"old-delete"`, "ACTIVE"},
			{"legacy", `"product_state":"PENDING"`, "PENDING"},
		} {
			for _, command := range []string{"list", "show"} {
				t.Run(product+"/"+tc.name+"/"+command, func(t *testing.T) {
					instance := fmt.Sprintf(`{"id":"inst_cli","display_name":"demo","engine":%q,%s}`, product, tc.fields)
					branchCalls := 0
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method != "GET" {
							t.Errorf("unexpected mutation %s", r.Method)
							return
						}
						switch r.URL.Path {
						case "/api/v1/instances":
							fmt.Fprintf(w, `{"items":[%s],"total":1,"page":1,"total_pages":1}`, instance)
						case "/api/v1/instances/inst_cli":
							fmt.Fprint(w, instance)
						case "/api/v1/instances/inst_cli/branches/main":
							branchCalls++
							if tc.want == "DELETING" || tc.want == "DELETED" {
								http.Error(w, `{"error":{"code":"BRANCH_NOT_FOUND"}}`, 404)
								return
							}
							fmt.Fprint(w, `{"instance_id":"inst_cli","branch":{"branch_id":"main","name":"main","root":true}}`)
						default:
							t.Errorf("unexpected path %s", r.URL.Path)
							http.NotFound(w, r)
						}
					}))
					defer server.Close()
					env := newTestEnv(t, server.URL)
					saveTestCredential(t, server.URL, env.credentialsPath, "usr_state")
					args := []string{product, command}
					if command == "show" {
						args = append(args, "inst_cli")
					}
					var out, diag bytes.Buffer
					code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diag)
					if code != 0 {
						t.Fatalf("code=%d diag=%s", code, &diag)
					}
					if command == "list" {
						rows := strings.Split(strings.TrimSpace(out.String()), "\n")
						if len(rows) != 2 {
							t.Fatalf("output=%s", &out)
						}
						fields := strings.Fields(rows[1])
						if len(fields) < 4 || fields[3] != tc.want {
							t.Fatalf("want %s, output=%s", tc.want, &out)
						}
					} else if !strings.Contains(out.String(), "Product state:  "+tc.want+"\n") {
						t.Fatalf("want %s, output=%s", tc.want, &out)
					}
					if (tc.want == "DELETING" || tc.want == "DELETED") && branchCalls != 0 {
						t.Fatal("deletion detail depended on removed branch")
					}
				})
			}
		}
	}
}
