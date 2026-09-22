//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

type deletePromptWriter struct {
	bytes.Buffer
	prompted chan struct{}
}

func (w *deletePromptWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	if bytes.Contains(p, []byte("[y/N]")) {
		w.prompted <- struct{}{}
	}
	return n, err
}

func TestInstanceDeleteTerminalConfirmation(t *testing.T) {
	for _, product := range []string{"sqlite", "git"} {
		for _, tc := range []struct {
			name, input           string
			wantCode, wantDeletes int
		}{
			{"yes", "yes\n", 0, 1}, {"short", "Y\n", 0, 1}, {"enter", "\n", 0, 0}, {"no", "n\n", 0, 0},
			{"other", "yes please\n", 0, 0}, {"eof", "\x04", 0, 0}, {"interrupt", "", 130, 0}, {"too long", strings.Repeat("y", 65) + "\n", 1, 0},
		} {
			t.Run(product+"/"+tc.name, func(t *testing.T) {
				master, slave := openDeleteTestPTY(t)
				defer master.Close()
				defer slave.Close()
				var deletes atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "DELETE" {
						deletes.Add(1)
						w.WriteHeader(202)
						fmt.Fprint(w, `{"instance_id":"inst_cli","operation_id":"op-delete"}`)
						return
					}
					json.NewEncoder(w).Encode(authclient.Instance{ID: "inst_cli", Engine: product, DisplayName: "demo\x1b[31m"})
				}))
				defer server.Close()
				env := newTestEnv(t, server.URL)
				saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var out bytes.Buffer
				diag := &deletePromptWriter{prompted: make(chan struct{}, 1)}
				done := make(chan int, 1)
				go func() { done <- runCLI(ctx, []string{product, "delete", "inst_cli"}, slave, &out, diag) }()
				select {
				case <-diag.prompted:
				case code := <-done:
					t.Fatalf("exited before confirmation: %d %s", code, diag.String())
				case <-time.After(3 * time.Second):
					t.Fatal("no confirmation prompt")
				}
				if deletes.Load() != 0 {
					t.Fatal("deleted before confirmation")
				}
				if tc.name == "interrupt" {
					cancel()
				} else if _, err := master.WriteString(tc.input); err != nil {
					t.Fatal(err)
				}
				select {
				case code := <-done:
					if code != tc.wantCode || int(deletes.Load()) != tc.wantDeletes {
						t.Fatalf("code=%d deletes=%d diag=%s", code, deletes.Load(), diag.String())
					}
					if !strings.Contains(diag.String(), "inst_cli") || strings.Contains(diag.String(), "\x1b") {
						t.Fatalf("unsafe/missing target: %s", diag.String())
					}
					if tc.wantDeletes == 0 && out.Len() != 0 {
						t.Fatalf("false success output: %s", &out)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("confirmation did not complete")
				}
			})
		}
	}
}
