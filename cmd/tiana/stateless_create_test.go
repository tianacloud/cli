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

	"github.com/tianacloud/cli/internal/authclient"
)

func TestCreateReturnsReceiptWithoutTokenOrPolling(t *testing.T) {
	for _, product := range []string{"sqlite", "git"} {
		t.Run(product, func(t *testing.T) {
			creates := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/v1/app-types":
					io.WriteString(w, appTypesResponse(product))
				case r.URL.Path == "/api/v1/instances" && r.Method == "POST":
					creates++
					w.WriteHeader(202)
					fmt.Fprint(w, `{"instance_id":"inst_cli","operation_id":"17"}`)
				default:
					t.Errorf("unexpected follow-up %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
			os.WriteFile(env.tokensPath, []byte("malformed old cache"), 0600)
			var out, diag bytes.Buffer
			code := runCLI(context.Background(), []string{product, "create", "demo"}, strings.NewReader(""), &out, &diag)
			if code != 0 || creates != 1 || !strings.Contains(out.String(), "17") || strings.Contains(out.String(), "Token") {
				t.Fatalf("code=%d out=%s diag=%s", code, &out, &diag)
			}
			data, _ := os.ReadFile(env.tokensPath)
			if string(data) != "malformed old cache" {
				t.Fatal("cache changed")
			}
			if _, err := authclient.NewFilePendingCommandStore(env.pendingPath).Load(); err != authclient.ErrPendingNotFound {
				t.Fatalf("accepted intent not finished: %v", err)
			}
		})
	}
}

type failedReceiptWriter struct{}

func (failedReceiptWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestAsyncCreationRecoveryAndConflictingIntent(t *testing.T) {
	for _, product := range []string{"sqlite", "git"} {
		for _, mode := range []string{"unknown", "output-failure", "other-account", "pending-conflict"} {
			t.Run(product+"/"+mode, func(t *testing.T) {
				writes, reads := 0, 0
				var requestIDs []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/v1/app-types" {
						reads++
						io.WriteString(w, appTypesResponse(product))
						return
					}
					if r.Method != "POST" || r.URL.Path != "/api/v1/instances" {
						t.Errorf("unexpected request %s %s", r.Method, r.URL)
						http.NotFound(w, r)
						return
					}
					writes++
					var body authclient.CreateInstanceRequest
					if e := json.NewDecoder(r.Body).Decode(&body); e != nil || body.RequestID == "" {
						t.Error("missing request identity")
					}
					requestIDs = append(requestIDs, body.RequestID)
					if mode == "unknown" && writes == 1 {
						w.WriteHeader(503)
						fmt.Fprint(w, `{"error":{"code":"UNAVAILABLE"}}`)
						return
					}
					w.WriteHeader(202)
					fmt.Fprint(w, `{"instance_id":"inst_cli","operation_id":"17"}`)
				}))
				defer server.Close()
				env := newTestEnv(t, server.URL)
				saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
				store := authclient.NewFilePendingCommandStore(env.pendingPath)
				key := "db.create"
				if product == "git" {
					key = "git.create"
				}
				blocked := mode == "other-account" || mode == "pending-conflict"
				var before []byte
				if blocked {
					p := authclient.PendingCommand{Command: key, Args: []string{"create", "demo"}, Origin: server.URL, UserID: "usr_cli", IdempotencyKey: "stable"}
					if mode == "other-account" {
						p.UserID = "other"
					}
					if mode == "pending-conflict" {
						p.Args = []string{"create", "another"}
					}
					if e := store.Save(p); e != nil {
						t.Fatal(e)
					}
					before, _ = os.ReadFile(env.pendingPath)
				}
				var out, diag bytes.Buffer
				var output io.Writer = &out
				if mode == "output-failure" {
					output = failedReceiptWriter{}
				}
				args := []string{product, "create", "demo"}
				if code := runCLI(context.Background(), args, strings.NewReader(""), output, &diag); code != 1 {
					t.Fatalf("code=%d diag=%s", code, &diag)
				}
				saved, e := store.Load()
				if e != nil {
					t.Fatal("lost recovery intent", e)
				}
				if blocked {
					after, _ := os.ReadFile(env.pendingPath)
					if writes != 0 || reads != 0 || !bytes.Equal(before, after) {
						t.Fatal("blocked intent modified")
					}
					return
				}
				if mode == "output-failure" && (saved.InstanceID != "inst_cli" || saved.CreationOperationID != "17") {
					t.Fatal("receipt not saved before output")
				}
				out.Reset()
				diag.Reset()
				if code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diag); code != 0 {
					t.Fatalf("resume code=%d diag=%s", code, &diag)
				}
				if mode == "unknown" {
					if writes != 2 || requestIDs[0] != requestIDs[1] {
						t.Fatal("changed request identity")
					}
				} else if writes != 1 {
					t.Fatal("replayed accepted creation")
				}
				if !strings.Contains(out.String(), "17") {
					t.Fatal("missing accepted operation")
				}
			})
		}
	}
}
