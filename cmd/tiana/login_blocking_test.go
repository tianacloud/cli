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

func TestBlockingLoginTerminalOutcomes(t *testing.T) {
	for _, mode := range []string{"approved", "denied", "expired", "completed", "timeout", "network-failure", "save-failure", "cancelled"} {
		for _, machine := range []bool{false, true} {
			name := mode
			if machine {
				name += "/json"
			}
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var credentialPath string
				exchanges := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/api/v1/auth/transactions":
						expires := 600
						if mode == "timeout" {
							expires = 1
						}
						json.NewEncoder(w).Encode(map[string]any{"transaction_id": "fixture", "client_secret": "synthetic-secret", "user_code": "TEST", "verification_uri_complete": "https://console.example.test/approve", "expires_in": expires, "poll_interval": 1})
					case "/api/v1/auth/transactions/fixture/poll":
						switch mode {
						case "denied", "expired", "completed":
							json.NewEncoder(w).Encode(map[string]string{"status": mode})
						case "timeout":
							io.WriteString(w, `{"status":"pending"}`)
						case "network-failure":
							w.WriteHeader(503)
						case "cancelled":
							cancel()
							io.WriteString(w, `{"status":"pending"}`)
						default:
							io.WriteString(w, `{"status":"approved","authorization_code":"synthetic-code"}`)
						}
					case "/api/v1/auth/token":
						exchanges++
						if mode == "save-failure" {
							if err := os.MkdirAll(credentialPath, 0700); err != nil {
								t.Error(err)
							}
						}
						io.WriteString(w, `{"access_token":"synthetic-access","refresh_token":"synthetic-refresh","expires_in":3600,"user":{"user_id":"owner","tenant_id":"tenant"}}`)
					default:
						t.Errorf("unexpected request: %s", r.URL.Path)
						w.WriteHeader(404)
					}
				}))
				defer server.Close()
				env := newTestEnv(t, server.URL)
				credentialPath = env.credentialsPath
				args := []string{"login", "--no-open"}
				if machine {
					args = append(args, "--json")
				}
				var out, diag bytes.Buffer
				code := runCLI(ctx, args, nil, &out, &diag)
				want := 1
				if mode == "approved" {
					want = 0
				}
				if mode == "cancelled" {
					want = 130
				}
				if machine && (mode == "denied" || mode == "expired" || mode == "timeout") {
					want = 5
				}
				if machine && mode == "completed" {
					want = 4
				}
				if code != want {
					t.Fatalf("exit=%d want=%d stdout=%s stderr=%s", code, want, &out, &diag)
				}
				if machine {
					got := readLoginJSON(t, &out)
					if mode == "approved" {
						if got.Status != "succeeded" || got.Data["logged_in"] != true {
							t.Fatalf("unexpected success: %s", &out)
						}
					} else {
						wantStatus := "failed"
						if mode == "completed" {
							wantStatus = "unknown"
						}
						if got.Status != wantStatus || got.Error.Code == "" || got.Data["logged_in"] != nil {
							t.Fatalf("unexpected failure: %s", &out)
						}
					}
					if strings.Contains(out.String(), "https://") || !strings.Contains(diag.String(), "https://console.example.test/approve") {
						t.Fatal("progress and final JSON not separated")
					}
				} else if !strings.Contains(out.String(), "Open:\nhttps://console.example.test/approve\n\nWaiting for authentication...\n") {
					t.Fatal("blocking text login output changed")
				}
				for _, secret := range []string{"synthetic-secret", "synthetic-code", "synthetic-access", "synthetic-refresh"} {
					if strings.Contains(out.String()+diag.String(), secret) {
						t.Fatal("authorization secret disclosed")
					}
				}
				if mode == "approved" {
					if _, err := authclient.NewFileStore(credentialPath, server.URL).Load(); err != nil || exchanges != 1 {
						t.Fatalf("success before durable save: %v, exchanges=%d", err, exchanges)
					}
				} else if mode != "save-failure" {
					if _, err := os.Stat(credentialPath); !os.IsNotExist(err) {
						t.Fatal("failed login wrote credentials")
					}
				}
			})
		}
	}
}

func TestLoginProgressOpensCompleteURIOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var out bytes.Buffer
	var opened []string
	w := &loginProgressWriter{Writer: &out, cancel: cancel, openBrowser: func(uri string) { opened = append(opened, uri) }}
	for _, chunk := range []string{"Open:\nhttps://console.", "example.test/approve?code=a&b=c", "\n\nWaiting for authentication...\n", "✓ Signed in as https://user.example.test\n"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if len(opened) != 1 || opened[0] != "https://console.example.test/approve?code=a&b=c" || ctx.Err() != nil {
		t.Fatalf("browser calls: %v", opened)
	}
}

func TestBlockingLoginJSONProgressFailure(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/auth/transactions" {
			t.Errorf("failed progress did not cancel polling: %s", r.URL.Path)
		}
		io.WriteString(w, `{"transaction_id":"fixture","client_secret":"synthetic-secret","user_code":"TEST","verification_uri_complete":"https://console.example.test/approve","expires_in":600,"poll_interval":1}`)
	}))
	defer server.Close()
	newTestEnv(t, server.URL)
	var out bytes.Buffer
	code := runCLI(t.Context(), []string{"login", "--json", "--no-open"}, nil, &out, failedReceiptWriter{})
	if code != 1 || calls != 1 || readLoginJSON(t, &out).Status != "failed" {
		t.Fatalf("code=%d calls=%d output=%s", code, calls, &out)
	}
}
