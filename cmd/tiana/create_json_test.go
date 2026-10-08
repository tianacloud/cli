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
)

func TestInstanceCreateJSON(t *testing.T) {
	for _, product := range []string{"sqlite", "git"} {
		for _, mode := range []string{"accepted", "succeeded", "failure"} {
			t.Run(product+"/"+mode, func(t *testing.T) {
				posts := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/api/v1/app-types":
						io.WriteString(w, appTypesResponse(product))
					case "/api/v1/instances":
						posts++
						if mode == "failure" {
							w.WriteHeader(403)
							io.WriteString(w, `{"error":{"code":"FORBIDDEN"}}`)
							return
						}
						w.WriteHeader(202)
						io.WriteString(w, `{"instance_id":"inst_cli","job_id":17}`)
					case "/api/v1/jobs/17":
						w.WriteHeader(404)
						io.WriteString(w, `{"error":{"code":"NOT_FOUND"}}`)
					case "/api/v1/instances/inst_cli":
						fmt.Fprintf(w, `{"id":"inst_cli","engine":%q,"product_state":"ACTIVE"}`, product)
					default:
						t.Errorf("unexpected path %s", r.URL.Path)
						w.WriteHeader(404)
					}
				}))
				defer server.Close()
				env := newTestEnv(t, server.URL)
				saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
				args := []string{product, "create", "demo", "--json"}
				if mode == "succeeded" {
					args = append(args, "--wait")
				}
				var out, diag bytes.Buffer
				code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diag)
				var result struct {
					Status string
					Data   struct {
						InstanceID string `json:"instance_id"`
						JobID      uint64 `json:"job_id"`
					}
					Error any
				}
				decoder := json.NewDecoder(&out)
				if err := decoder.Decode(&result); err != nil {
					t.Fatalf("invalid JSON: %v stderr=%s", err, &diag)
				}
				if err := decoder.Decode(new(any)); err != io.EOF {
					t.Fatalf("extra stdout: %v", err)
				}
				if posts != 1 {
					t.Fatalf("posts=%d", posts)
				}
				if mode == "failure" {
					if code == 0 || result.Status != "failed" || result.Error == nil {
						t.Fatalf("failure: %+v code=%d", result, code)
					}
					return
				}
				if code != 0 || result.Status != mode || result.Data.InstanceID != "inst_cli" || result.Data.JobID != 17 {
					t.Fatalf("result=%+v code=%d stderr=%s", result, code, &diag)
				}
			})
		}
	}
}

func TestCreateIdentityIgnoresJSONFormat(t *testing.T) {
	got := createIdentityArguments([]string{"--json", "--wait", "-m", "--json", "--", "--json"})
	if !sameStrings(got, []string{"-m", "--json", "--", "--json"}) {
		t.Fatalf("identity=%q", got)
	}
}
