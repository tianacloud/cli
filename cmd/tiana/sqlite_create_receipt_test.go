package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/tianacloud/cli/internal/authclient"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func runSQLiteCreateForTest(args []string) (int, string, string) {
	var out, diag bytes.Buffer
	code := runSQLite(context.Background(), args, strings.NewReader(""), &out, &diag)
	return code, out.String(), diag.String()
}
func TestSQLiteCreateAcceptedOperationRecovery(t *testing.T) {
	for _, mode := range []string{"success", "interrupted", "not-visible", "failed", "wrong-operation", "running", "legacy-missing-id"} {
		t.Run(mode, func(t *testing.T) {
			creates, tokens, polls, gets := 0, 0, 0, 0
			completed := false
			var pendingPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/v1/app-types":
					io.WriteString(w, appTypesResponse("sqlite"))
				case r.URL.Path == "/api/v1/instances" && r.Method == "POST":
					creates++
					if mode == "legacy-missing-id" && r.Header.Get("Idempotency-Key") != "existing-create-key" {
						t.Error("recovery replaced create idempotency key")
					}
					w.WriteHeader(202)
					fmt.Fprintf(w, `{"instance_id":%q,"operation_id":"17"}`, testInstanceID)
				case r.URL.Path == "/api/v1/instances/"+testInstanceID && r.Method == "GET":
					gets++
					if mode == "not-visible" && gets <= 2 {
						w.WriteHeader(404)
						io.WriteString(w, `{"error":{"code":"INSTANCE_NOT_FOUND"}}`)
						return
					}
					if completed {
						io.WriteString(w, strings.TrimSuffix(sqliteInstanceResponse(), "}")+`,"creation_operation_id":"17"}`)
					} else {
						fmt.Fprintf(w, `{"id":%q,"display_name":"sqlite-db","engine":"sqlite","creation_operation_id":"17","product_state":"CREATING"}`, testInstanceID)
					}
				case r.URL.Path == "/api/v1/instances/"+testInstanceID+"/operations/17" && r.Method == "GET":
					polls++
					pending, err := authclient.NewFilePendingCommandStore(pendingPath).Load()
					if err != nil || pending.InstanceID != testInstanceID || pending.Step != "instance" || pending.OperationID != "" {
						t.Error("accepted instance not persisted separately from Token recovery")
					}
					if mode == "interrupted" && polls == 1 {
						http.Error(w, `{"error":{"code":"UNAVAILABLE"}}`, 503)
						return
					}
					state, op := "success", "17"
					if mode == "running" && polls == 1 {
						state = "running"
					}
					if mode == "failed" {
						state = "failed"
					}
					if mode == "wrong-operation" {
						op = "18"
					}
					completed = state == "success" && op == "17"
					fmt.Fprintf(w, `{"instance_id":%q,"operation_id":%q,"kind":"CREATE_INSTANCE","state":%q}`, testInstanceID, op, state)
				case r.URL.Path == "/api/v1/instances/"+testInstanceID+"/endpoints/"+testEndpointID+"/tokens" && r.Method == "POST":
					if !completed {
						t.Error("Token before successful creation")
					}
					tokens++
					if mode == "legacy-missing-id" && r.Header.Get("Idempotency-Key") != "existing-token-key" {
						t.Error("recovery replaced token idempotency key")
					}
					if mode == "legacy-missing-id" {
						var request authclient.CreateTokenRequest
						if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.RequestID != "existing-request" || request.ExpiresAt != -1 {
							t.Error("recovery changed token request identity or expiration")
						}
					}
					w.WriteHeader(201)
					io.WriteString(w, tokenCreateResponse())
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			pendingPath = env.pendingPath
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_sqlite")
			args := []string{"create", "sqlite-db"}
			if mode == "legacy-missing-id" {
				err := authclient.NewFilePendingCommandStore(env.pendingPath).Save(authclient.PendingCommand{Command: "db.create", Args: args, Origin: server.URL, UserID: "usr_sqlite", Step: "token", IdempotencyKey: "existing-create-key", TokenIdempotencyKey: "existing-token-key", TokenRequestID: "existing-request", ExpiresAt: -1})
				if err != nil {
					t.Fatal(err)
				}
			}
			code, out, diag := runSQLiteCreateForTest(args)
			if mode == "interrupted" || mode == "not-visible" {
				if code != 1 || tokens != 0 {
					t.Fatalf("first code=%d tokens=%d", code, tokens)
				}
				code, out, diag = runSQLiteCreateForTest(args)
				if mode == "not-visible" {
					if code != 1 {
						t.Fatalf("second code=%d", code)
					}
					saved, err := authclient.NewFilePendingCommandStore(env.pendingPath).Load()
					if err != nil || saved.InstanceID != testInstanceID {
						t.Fatal("transient read discarded accepted creation")
					}
					code, out, diag = runSQLiteCreateForTest(args)
				}
			}
			if mode == "failed" || mode == "wrong-operation" {
				if code != 1 || tokens != 0 || out != "" {
					t.Fatalf("failure code=%d tokens=%d", code, tokens)
				}
				pending, err := authclient.NewFilePendingCommandStore(env.pendingPath).Load()
				if err != nil || pending.InstanceID != testInstanceID || pending.Step != "instance" {
					t.Fatal("lost accepted creation")
				}
			} else if code != 0 || tokens != 1 || !strings.Contains(out, "https://") {
				t.Fatalf("code=%d tokens=%d diag=%s", code, tokens, diag)
			}
			if creates != 1 || polls == 0 || gets == 0 {
				t.Fatalf("creates=%d polls=%d gets=%d", creates, polls, gets)
			}
		})
	}
}
