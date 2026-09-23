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

func TestCreateQueuedJobReceiptAndRecovery(t *testing.T) {
	for _, product := range []string{"git", "sqlite"} {
		for _, mode := range []string{"fresh", "stdout-failure", "old-rejected-receipt"} {
			t.Run(product+"/"+mode, func(t *testing.T) {
				posts := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/v1/app-types" {
						io.WriteString(w, appTypesResponse(product))
						return
					}
					if r.Method != "POST" || r.URL.Path != "/api/v1/instances" {
						t.Errorf("unexpected follow-up %s %s", r.Method, r.URL.Path)
						http.NotFound(w, r)
						return
					}
					posts++
					var body authclient.CreateInstanceRequest
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RequestID == "" {
						t.Error("missing request ID")
						return
					}
					if mode == "old-rejected-receipt" && body.RequestID != "already-created" {
						t.Error("did not reuse original request")
					}
					w.WriteHeader(http.StatusAccepted)
					fmt.Fprint(w, `{"instance_id":"inst_cli","job_id":9007199254740993,"operation_id":""}`)
				}))
				defer server.Close()
				env := newTestEnv(t, server.URL)
				saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
				store := authclient.NewFilePendingCommandStore(env.pendingPath)
				if mode == "old-rejected-receipt" {
					key := "git.create"
					if product == "sqlite" {
						key = "db.create"
					}
					if err := store.Save(authclient.PendingCommand{Command: key, Args: []string{"create", "demo"}, Origin: server.URL, UserID: "usr_cli", IdempotencyKey: "already-created"}); err != nil {
						t.Fatal(err)
					}
				}
				var out, diag bytes.Buffer
				args := []string{product, "create", "demo"}
				if mode == "stdout-failure" {
					if code := runCLI(context.Background(), args, strings.NewReader(""), failedReceiptWriter{}, &diag); code != 1 {
						t.Fatalf("code=%d", code)
					}
					pending, err := store.Load()
					if err != nil || pending.InstanceID != "inst_cli" || pending.CreationJobID != 9007199254740993 {
						t.Fatalf("receipt not persisted: %v", err)
					}
					diag.Reset()
				}
				if code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diag); code != 0 {
					t.Fatalf("code=%d diag=%s", code, &diag)
				}
				if posts != 1 || !strings.Contains(out.String(), "job=9007199254740993") || strings.Contains(out.String(), "operation=") {
					t.Fatalf("posts=%d output=%s", posts, &out)
				}
				if _, err := store.Load(); err != authclient.ErrPendingNotFound {
					t.Fatalf("pending not cleared: %v", err)
				}
			})
		}
	}
}
