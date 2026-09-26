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

func TestAppsCreateUsesExistingLoginCredential(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		called = true
		if req.Method != "PUT" || req.URL.Path != "/api/v1/web-projects/billing" || req.Header.Get("Authorization") != "Bearer account-access" {
			t.Errorf("wrong authenticated project request")
		}
		io.WriteString(w, `{"project_id":"billing","name":"Billing"}`)
	}))
	defer server.Close()
	t.Setenv("TIANA_MGR_ORIGIN", server.URL)
	credentials := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv("TIANA_CREDENTIALS_FILE", credentials)
	if err := authclient.NewFileStore(credentials, server.URL).Save(authclient.Credential{AccessToken: "account-access", RefreshToken: "account-refresh", ExpiresAt: time.Now().Add(time.Hour), User: authclient.User{ID: "user-a"}}); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	code := runCLI(context.Background(), []string{"apps", "create", "--project", "billing", "--name", "Billing", "--json"}, nil, &out, &diagnostics)
	if code != 0 || !called {
		t.Fatalf("apps unavailable: code=%d stderr=%s stdout=%s", code, &diagnostics, &out)
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			ProjectID string `json:"project_id"`
		} `json:"data"`
	}
	if json.Unmarshal(out.Bytes(), &body) != nil || body.Status != "succeeded" || body.Data.ProjectID != "billing" {
		t.Fatalf("unexpected JSON: %s", &out)
	}
}

func TestAppsHelpAndValidation(t *testing.T) {
	for _, args := range [][]string{{"apps", "--help"}, {"apps", "upload", "--help"}} {
		var out, diagnostics bytes.Buffer
		if code := runCLI(context.Background(), args, nil, &out, &diagnostics); code != 0 {
			t.Fatalf("help: %d %s", code, &diagnostics)
		}
	}
	var out, diagnostics bytes.Buffer
	if code := runCLI(context.Background(), []string{"apps", "upload", "--project", "bad/project", "--dir", "dist"}, nil, &out, &diagnostics); code != 2 {
		t.Fatalf("invalid project code=%d", code)
	}
}
