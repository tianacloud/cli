//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBranchDeleteConfirmation(t *testing.T) {
	for _, answer := range []string{"yes\n", "n\n", "\n", "cancel"} {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			master, slave := openDeleteTestPTY(t)
			defer master.Close()
			defer slave.Close()
			var deletes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "DELETE" {
					deletes.Add(1)
					if r.URL.Path != "/api/v1/instances/inst_cli/branches/child" {
						t.Errorf("unconfirmed target %s", r.URL)
					}
					w.WriteHeader(202)
					fmt.Fprint(w, `{"instance_id":"inst_cli","operation_id":"op-delete"}`)
					return
				}
				switch r.URL.Path {
				case "/api/v1/instances/inst_cli":
					fmt.Fprint(w, `{"id":"inst_cli","engine":"sqlite"}`)
				case "/api/v1/instances/inst_cli/branches":
					fmt.Fprint(w, `{"items":[{"branch_id":"child","name":"preview"}]}`)
				case "/api/v1/instances/inst_cli/branches/child":
					fmt.Fprint(w, `{"instance_id":"inst_cli","branch":{"branch_id":"child","name":"preview"}}`)
				default:
					t.Errorf("unexpected read %s", r.URL)
				}
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var out bytes.Buffer
			diag := &deletePromptWriter{prompted: make(chan struct{}, 1)}
			done := make(chan int, 1)
			go func() {
				done <- runCLI(ctx, []string{"sqlite", "branch", "delete", "inst_cli", "preview"}, slave, &out, diag)
			}()
			select {
			case <-diag.prompted:
			case code := <-done:
				t.Fatalf("no prompt code=%d diag=%s", code, diag.String())
			case <-time.After(3 * time.Second):
				t.Fatal("prompt timeout")
			}
			if deletes.Load() != 0 {
				t.Fatal("deleted before confirmation")
			}
			if answer == "cancel" {
				cancel()
			} else {
				fmt.Fprint(master, answer)
			}
			select {
			case code := <-done:
				expectedCode := 0
				if answer == "cancel" {
					expectedCode = 130
				}
				expectedDeletes := int32(0)
				if answer == "yes\n" {
					expectedDeletes = 1
				}
				if code != expectedCode || deletes.Load() != expectedDeletes {
					t.Fatalf("code=%d deletes=%d diag=%s", code, deletes.Load(), diag.String())
				}
			case <-time.After(3 * time.Second):
				t.Fatal("confirmation stuck")
			}
		})
	}
}
