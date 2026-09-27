package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

func TestWebCreateUsesExistingLoginCredential(t *testing.T) {
	t.Setenv("TIANA_PENDING_COMMAND_FILE", filepath.Join(t.TempDir(), "pending.json"))
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		called = true
		if req.Method != "POST" || req.URL.Path != "/api/v1/web-projects" || req.Header.Get("Authorization") != "Bearer account-access" {
			t.Errorf("wrong authenticated project request")
		}
		io.WriteString(w, `{"id":"web-AAAAAAAAAAAAAAAAAAAAAAAA","name":"Billing","owner_id":"user-a","tenant_id":"ten-test"}`)
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
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(out.Bytes(), &body) != nil || body.Status != "succeeded" || body.Data.ID != "web-AAAAAAAAAAAAAAAAAAAAAAAA" {
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
	if code := runCLI(context.Background(), []string{"web", "upload", "bad/project", "--dir", "dist"}, nil, &out, &diagnostics); code != 2 {
		t.Fatalf("invalid project code=%d", code)
	}
}
