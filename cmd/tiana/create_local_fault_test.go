//go:build linux || darwin

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

	"github.com/tianacloud/cli/internal/authclient"
)

func TestCreationPersistencePermissionFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission fault requires unprivileged user")
	}
	for _, product := range []string{"sqlite", "git"} {
		for _, stage := range []string{"intent", "receipt"} {
			t.Run(product+"/"+stage, func(t *testing.T) {
				parent := t.TempDir()
				root := filepath.Join(parent, "state")
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
				defer os.Chmod(parent, 0700)
				posts := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/api/v1/app-types":
						if stage == "intent" {
							if err := os.Chmod(parent, 0000); err != nil {
								t.Error(err)
							}
						}
						fmt.Fprint(w, appTypesResponse(product))
					case "/api/v1/instances":
						posts++
						if stage == "receipt" {
							if err := os.Chmod(parent, 0000); err != nil {
								t.Error(err)
							}
						}
						w.WriteHeader(202)
						fmt.Fprint(w, `{"instance_id":"inst_cli","operation_id":"17"}`)
					default:
						t.Errorf("unexpected request %s", r.URL)
					}
				}))
				defer server.Close()
				env := newTestEnv(t, server.URL)
				saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
				path := filepath.Join(root, "pending.json")
				t.Setenv("TIANA_PENDING_COMMAND_FILE", path)
				var out, diag bytes.Buffer
				code := runCLI(context.Background(), []string{product, "create", "demo"}, strings.NewReader(""), &out, &diag)
				if err := os.Chmod(parent, 0700); err != nil {
					t.Fatal(err)
				}
				if code != 1 || out.Len() != 0 || diag.Len() == 0 {
					t.Fatalf("code=%d out=%s diag=%s", code, &out, &diag)
				}
				if stage == "intent" {
					if posts != 0 {
						t.Fatal("POST before durable identity")
					}
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("unexpected pending: %v", err)
					}
				} else {
					p, err := authclient.NewFilePendingCommandStore(path).Load()
					if posts != 1 || err != nil || p.IdempotencyKey == "" || p.InstanceID != "" {
						t.Fatalf("original intent not retained posts=%d err=%v", posts, err)
					}
				}
			})
		}
	}
}

func TestBranchCreationReceiptOutputFailure(t *testing.T) {
	posts, polls := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "/api/v1/instances/inst_cli"
		switch r.URL.Path {
		case base:
			fmt.Fprint(w, `{"id":"inst_cli","engine":"sqlite"}`)
		case base + "/branches/main":
			fmt.Fprint(w, `{"instance_id":"inst_cli","branch":{"branch_id":"main","name":"main","root":true}}`)
		case base + "/branches/main/children":
			posts++
			w.WriteHeader(202)
			fmt.Fprint(w, `{"instance_id":"inst_cli","operation_id":"42"}`)
		default:
			polls++
			t.Errorf("unexpected request %s", r.URL)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
	var diag bytes.Buffer
	code := runCLI(context.Background(), []string{"sqlite", "branch", "create", "inst_cli", "preview", "--wait"}, strings.NewReader(""), failedReceiptWriter{}, &diag)
	if code != 1 || posts != 1 || polls != 0 || !strings.Contains(diag.String(), "check branch list before retrying") {
		t.Fatalf("code=%d posts=%d polls=%d diag=%s", code, posts, polls, &diag)
	}
}
