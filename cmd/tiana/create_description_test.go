package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/tianacloud/cli/internal/authclient"
)

func TestCreateDescription(t *testing.T) {
	for _, product := range []string{"git", "sqlite"} {
		for _, tc := range []struct {
			name  string
			args  []string
			notes string
			code  int
		}{
			{"omitted", []string{"demo"}, "", 0},
			{"short", []string{"demo", "-m", "应用数据库"}, "应用数据库", 0},
			{"long", []string{"--message=repository description", "demo"}, "repository description", 0},
			{"whitespace", []string{"demo", "-m", " demo "}, " demo ", 0},
			{"lines", []string{"demo", "-m", "first\nsecond"}, "first\nsecond", 0},
			{"wait-value", []string{"-m", "-w", "demo"}, "-w", 0},
			{"separator-value", []string{"-m", "--", "demo"}, "--", 0},
			{"empty", []string{"demo", "-m", ""}, "", 0},
			{"boundary", []string{"demo", "-m", strings.Repeat("a", 2048)}, strings.Repeat("a", 2048), 0},
			{"too-long", []string{"demo", "-m", strings.Repeat("a", 2049)}, "", 2},
			{"utf8-byte-limit", []string{"demo", "-m", strings.Repeat("中", 683)}, "", 2},
			{"invalid-utf8", []string{"demo", "-m", string([]byte{0xff})}, "", 2},
			{"missing-value", []string{"demo", "-m"}, "", 2},
			{"duplicate", []string{"demo", "-m", "one", "--message", "two"}, "", 2},
		} {
			t.Run(product+"/"+tc.name, func(t *testing.T) {
				posts, calls := 0, 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					switch r.URL.Path {
					case "/api/v1/app-types":
						io.WriteString(w, appTypesResponse(product))
					case "/api/v1/instances":
						posts++
						var body authclient.CreateInstanceRequest
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Notes != tc.notes || body.Engine != product || body.DisplayName != "demo" {
							t.Errorf("description payload mismatch; err=%v", err)
						}
						w.WriteHeader(202)
						io.WriteString(w, `{"instance_id":"inst_cli","job_id":17}`)
					default:
						t.Errorf("unexpected %s", r.URL)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				env := newTestEnv(t, server.URL)
				saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
				var out, diag bytes.Buffer
				code := runCLI(context.Background(), append([]string{product, "create"}, tc.args...), strings.NewReader(""), &out, &diag)
				if code != tc.code {
					t.Fatalf("code=%d diag=%s", code, &diag)
				}
				if tc.code == 0 && posts != 1 {
					t.Fatalf("posts=%d", posts)
				}
				if tc.code == 2 && calls != 0 {
					t.Fatalf("invalid input sent requests=%d", calls)
				}
				if _, err := os.Stat(env.pendingPath); !os.IsNotExist(err) {
					t.Fatalf("pending remains: %v", err)
				}
			})
		}
	}
}

func TestCreateDescriptionRecovery(t *testing.T) {
	for _, product := range []string{"git", "sqlite"} {
		t.Run(product, func(t *testing.T) {
			var ids []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/app-types":
					io.WriteString(w, appTypesResponse(product))
				case "/api/v1/instances":
					var body authclient.CreateInstanceRequest
					json.NewDecoder(r.Body).Decode(&body)
					if body.Notes != "-w" {
						t.Error("recovery changed description")
					}
					ids = append(ids, body.RequestID)
					if len(ids) == 1 {
						w.WriteHeader(503)
						io.WriteString(w, `{"error":{"code":"UNAVAILABLE"}}`)
						return
					}
					w.WriteHeader(202)
					io.WriteString(w, `{"instance_id":"inst_cli","job_id":17}`)
				case "/api/v1/jobs/17":
					json.NewEncoder(w).Encode(map[string]any{"job_id": 17, "instance_id": "inst_cli", "request_id": ids[0], "job_kind": "instance_create", "status": "complete"})
				case "/api/v1/instances/inst_cli":
					json.NewEncoder(w).Encode(map[string]string{"id": "inst_cli", "engine": product, "product_state": "ACTIVE"})
				default:
					t.Errorf("unexpected %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
			run := func(tail ...string) int {
				var out, diag bytes.Buffer
				return runCLI(context.Background(), append([]string{product, "create"}, tail...), strings.NewReader(""), &out, &diag)
			}
			if code := run("demo", "-m", "-w"); code != 1 || len(ids) != 1 {
				t.Fatalf("first code=%d posts=%d", code, len(ids))
			}
			before, err := os.ReadFile(env.pendingPath)
			if err != nil {
				t.Fatal(err)
			}
			if code := run("demo", "-m", "--wait"); code != 1 || len(ids) != 1 {
				t.Fatalf("changed notes accepted: code=%d posts=%d", code, len(ids))
			}
			after, _ := os.ReadFile(env.pendingPath)
			if !bytes.Equal(before, after) {
				t.Fatal("pending overwritten")
			}
			if code := run("demo", "-m", "-w", "--wait"); code != 0 || len(ids) != 2 || ids[0] == "" || ids[0] != ids[1] {
				t.Fatalf("resume code=%d ids=%v", code, ids)
			}
		})
	}
}
