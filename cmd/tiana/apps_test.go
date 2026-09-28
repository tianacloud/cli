package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

func TestWebCreateUsesExistingLoginCredential(t *testing.T) {
	t.Setenv("TIANA_PENDING_COMMAND_FILE", filepath.Join(t.TempDir(), "pending.json"))
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		called = true
		if req.Method != "POST" || req.URL.Path != "/api/v1/apps" || req.Header.Get("Authorization") != "Bearer account-access" {
			t.Errorf("wrong authenticated App request")
		}
		io.WriteString(w, `{"app_id":"AAAAAAAAAAAA","name":"Billing","owner_id":"user-a","tenant_id":"ten-test"}`)
	}))
	defer server.Close()
	t.Setenv("TIANA_API_ORIGIN", server.URL)
	credentials := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv("TIANA_CREDENTIALS_FILE", credentials)
	if err := authclient.NewFileStore(credentials, server.URL).Save(authclient.Credential{AccessToken: "account-access", RefreshToken: "account-refresh", ExpiresAt: time.Now().Add(time.Hour), User: authclient.User{ID: "user-a"}}); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	code := runCLI(context.Background(), []string{"web", "create", "Billing", "--json"}, nil, &out, &diagnostics)
	if code != 0 || !called {
		t.Fatalf("web unavailable: code=%d stderr=%s stdout=%s", code, &diagnostics, &out)
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			AppID string `json:"app_id"`
		} `json:"data"`
	}
	if json.Unmarshal(out.Bytes(), &body) != nil || body.Status != "succeeded" || body.Data.AppID != "AAAAAAAAAAAA" {
		t.Fatalf("unexpected JSON: %s", &out)
	}
}

func TestWebHelpAndValidation(t *testing.T) {
	for _, args := range [][]string{{"web", "--help"}, {"web", "upload", "--help"}} {
		var out, diagnostics bytes.Buffer
		if code := runCLI(context.Background(), args, nil, &out, &diagnostics); code != 0 {
			t.Fatalf("help: %d %s", code, &diagnostics)
		}
	}
	var out, diagnostics bytes.Buffer
	if code := runCLI(context.Background(), []string{"web", "upload", "bad/app", "--dir", "dist"}, nil, &out, &diagnostics); code != 2 {
		t.Fatalf("invalid App code=%d", code)
	}
}

func TestWebFailureRetainsRequestID(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		t.Run(fmt.Sprint(jsonMode), func(t *testing.T) {
			requestID := ""
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestID = r.Header.Get("X-Request-ID")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(503)
				io.WriteString(w, `{"error":{"code":"TEMPORARY_FAILURE","message":"try again"}}`)
			}))
			defer server.Close()
			t.Setenv("TIANA_API_ORIGIN", server.URL)
			credentials := filepath.Join(t.TempDir(), "credentials.json")
			t.Setenv("TIANA_CREDENTIALS_FILE", credentials)
			if err := authclient.NewFileStore(credentials, server.URL).Save(authclient.Credential{AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour), User: authclient.User{ID: "user-a"}}); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TIANA_PENDING_COMMAND_FILE", filepath.Join(t.TempDir(), "pending.json"))
			args := []string{"web", "create", "billing"}
			if jsonMode {
				args = append(args, "--json")
			}
			var out, diagnostics bytes.Buffer
			code := runCLI(context.Background(), args, nil, &out, &diagnostics)
			if code != 4 || requestID == "" {
				t.Fatalf("code=%d requestID=%q", code, requestID)
			}
			if jsonMode {
				var result struct {
					Error struct {
						RequestID string `json:"request_id"`
					} `json:"error"`
				}
				if err := json.Unmarshal(out.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Error.RequestID != requestID {
					t.Fatalf("JSON lost request ID: %s", &out)
				}
			} else if !strings.Contains(diagnostics.String(), "Request ID: "+requestID) {
				t.Fatalf("stderr lost request ID: %s", &diagnostics)
			}
		})
	}
}
