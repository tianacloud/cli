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

func TestTokenEndpointRecovery(t *testing.T) {
	for _, command := range []string{"db.create", "db.tokens.create"} {
		for _, scenario := range []string{"legacy", "pinned", "changed", "missing", "readback"} {
			t.Run(command+"/"+scenario, func(t *testing.T) {
				endpoint := testEndpointID
				if scenario == "missing" || scenario == "readback" {
					endpoint = ""
				}
				args := []string{"create", "sqlite-db"}
				if command == "db.tokens.create" {
					args = []string{"tokens", "create", testInstanceID}
				}
				posts, reads := 0, 0
				var store *authclient.FilePendingCommandStore
				// The route pattern is copied from MGR productionhttp, not built
				// from the client URL. The old instance-only route must fail.
				mux := http.NewServeMux()
				mux.HandleFunc("GET /api/v1/instances/{instance_id}", func(w http.ResponseWriter, r *http.Request) {
					io.WriteString(w, instanceResponse(testInstanceID, "sqlite-db", endpoint))
				})
				mux.HandleFunc("GET /api/v1/instances/{instance_id}/branches/main", func(w http.ResponseWriter, r *http.Request) {
					io.WriteString(w, sqliteBranchResponse(testInstanceID, "main", "production", endpoint))
				})
				expectedEndpoint := testEndpointID
				if command == "db.tokens.create" && scenario == "changed" {
					expectedEndpoint = "ep-1abcdefghjkmnpqrstvwxyz012"
				}

				mux.HandleFunc("POST /api/v1/instances/{instance_id}/endpoints/{endpoint_id}/tokens", func(w http.ResponseWriter, r *http.Request) {
					posts++
					if r.PathValue("instance_id") != testInstanceID || r.PathValue("endpoint_id") != expectedEndpoint {
						t.Error("wrong Token target")
					}
					var request authclient.CreateTokenRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if r.Header.Get("Idempotency-Key") != "" || request.RequestID != "request-id" || request.ExpiresAt != authclient.InstanceTokenNoExpiry {
						t.Error("recovery changed write identity")
					}
					pending, err := store.Load()
					if err != nil || pending.EndpointID != expectedEndpoint || pending.TokenRequestID != "request-id" {
						t.Error("endpoint/write identity not persisted before POST", err)
					}
					// Keep outcome unresolved to inspect the saved retry target.
					w.WriteHeader(http.StatusServiceUnavailable)
					io.WriteString(w, `{"error":{"code":"UNAVAILABLE","message":"temporary"}}`)
				})
				mux.HandleFunc("GET /api/v1/jobs/{job_id}", func(w http.ResponseWriter, r *http.Request) {
					reads++
					io.WriteString(w, `{"job_id":7,"request_id":"request-id","job_kind":"token_create","status":"running"}`)
				})
				server := httptest.NewServer(mux)
				defer server.Close()
				env := newTestEnv(t, server.URL)
				saveTestCredential(t, server.URL, env.credentialsPath, "usr_sqlite")
				store = authclient.NewFilePendingCommandStore(env.pendingPath)
				pending := authclient.PendingCommand{Command: command, Args: args, Origin: server.URL, UserID: "usr_sqlite", InstanceID: testInstanceID, IdempotencyKey: "instance-key", TokenIdempotencyKey: "token-key", TokenRequestID: "request-id", ExpiresAt: authclient.InstanceTokenNoExpiry, Step: "token"}
				if scenario != "legacy" {
					pending.EndpointID = testEndpointID
				}
				if scenario == "changed" {
					pending.EndpointID = "ep-1abcdefghjkmnpqrstvwxyz012"
				}
				if scenario == "readback" {
					pending.JobID = 7
					pending.TokenID = testTokenID
				}
				if err := store.Save(pending); err != nil {
					t.Fatal(err)
				}
				for attempt := 0; attempt < 2; attempt++ {
					var out, diag bytes.Buffer
					if code := runSQLite(context.Background(), args, strings.NewReader(""), &out, &diag); code != 1 || out.Len() != 0 {
						t.Fatalf("code=%d diagnostic=%s", code, &diag)
					}
					if command == "db.create" && scenario == "changed" && !strings.Contains(diag.String(), "differs from pending") {
						t.Fatalf("missing target mismatch diagnostic: %s", &diag)
					}
					if command == "db.create" && scenario == "missing" && !strings.Contains(diag.String(), "no endpoint_id") {
						t.Fatalf("missing endpoint diagnostic: %s", &diag)
					}
				}
				after, err := store.Load()
				wantEndpoint := pending.EndpointID
				if scenario == "legacy" {
					wantEndpoint = testEndpointID
				}
				if err != nil || after.EndpointID != wantEndpoint || after.TokenIdempotencyKey != pending.TokenIdempotencyKey || after.TokenRequestID != pending.TokenRequestID || after.ExpiresAt != pending.ExpiresAt || after.InstanceID != pending.InstanceID {
					t.Fatal("pending intent identity was lost", err)
				}
				wantPosts, wantReads := 0, 0
				if scenario == "legacy" || scenario == "pinned" || (command == "db.tokens.create" && (scenario == "changed" || scenario == "missing")) {
					wantPosts = 2
				}
				if scenario == "readback" {
					wantReads = 2
				}
				if posts != wantPosts || reads != wantReads {
					t.Fatalf("posts=%d reads=%d", posts, reads)
				}
			})
		}
	}
}

func TestFreshCreateWithoutEndpointPreservesInstance(t *testing.T) {
	creates, tokens := 0, 0
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/app-types", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, appTypesResponse("sqlite"))
	})
	mux.HandleFunc("POST /api/v1/instances", func(w http.ResponseWriter, r *http.Request) {
		creates++
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, instanceResponse(testInstanceID, "sqlite-db", ""))
	})
	mux.HandleFunc("GET /api/v1/instances/{instance_id}", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, sqliteInstanceResponse())
	})
	mux.HandleFunc("POST /api/v1/instances/{instance_id}/endpoints/{endpoint_id}/tokens", func(w http.ResponseWriter, r *http.Request) {
		tokens++
		if r.PathValue("endpoint_id") != testEndpointID {
			t.Error("wrong endpoint")
		}
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, tokenCreateResponse())
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_sqlite")
	for attempt, wantCode := range []int{1, 0} {
		var out, diag bytes.Buffer
		if code := runSQLite(context.Background(), []string{"create", "sqlite-db"}, strings.NewReader(""), &out, &diag); code != wantCode {
			t.Fatalf("attempt=%d code=%d diagnostic=%s", attempt, code, &diag)
		}
		if attempt == 0 {
			pending, err := authclient.NewFilePendingCommandStore(env.pendingPath).Load()
			if err != nil || pending.InstanceID != testInstanceID || pending.EndpointID != "" || tokens != 0 {
				t.Fatal("created instance intent lost or premature token write", err)
			}
		}
	}
	if creates != 1 || tokens != 1 {
		t.Fatal(fmt.Sprintf("duplicate writes: creates=%d tokens=%d", creates, tokens))
	}
}
