package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/tianacloud/cli/internal/testutil/account"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
)

func TestWebCreateUsesExistingLoginCredential(t *testing.T) {
	t.Setenv("TIANA_PENDING_COMMAND_FILE", filepath.Join(t.TempDir(), "pending.json"))
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		called = true
		if req.Method != "POST" || req.URL.Path != "/api/v1/web-projects" || req.Header.Get("Authorization") != "Bearer account-access" {
			t.Errorf("wrong authenticated Web request")
		}
		io.WriteString(w, `{"id":"AAAAAAAAAAAA","name":"Billing","owner_id":"user-a","tenant_id":"ten-test"}`)
	}))
	defer server.Close()
	t.Setenv("TIANA_API_ORIGIN", server.URL)
	credentials := account.CredentialPath(t)
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
	if json.Unmarshal(out.Bytes(), &body) != nil || body.Status != "succeeded" || body.Data.ID != "AAAAAAAAAAAA" {
		t.Fatalf("unexpected JSON: %s", &out)
	}
}

func TestWebHelpAndValidation(t *testing.T) {
	for _, args := range [][]string{{"web", "--help"}, {"web", "publish", "--help"}, {"web", "pull", "--help"}} {
		var out, diagnostics bytes.Buffer
		if code := runCLI(context.Background(), args, nil, &out, &diagnostics); code != 0 {
			t.Fatalf("help %v: %d %s", args, code, &diagnostics)
		}
	}
	var out, diagnostics bytes.Buffer
	if code := runCLI(context.Background(), []string{"web", "publish", "bad/app", "--dir", "dist"}, nil, &out, &diagnostics); code != 2 {
		t.Fatalf("invalid Web code=%d", code)
	}
	out.Reset()
	diagnostics.Reset()
	if code := runCLI(context.Background(), []string{"web", "pull", "bad/app", "--dir", "dist"}, nil, &out, &diagnostics); code != 2 {
		t.Fatalf("invalid Web pull ID code=%d", code)
	}
	out.Reset()
	diagnostics.Reset()
	if code := runCLI(context.Background(), []string{"web", "pull", "web-one"}, nil, &out, &diagnostics); code != 2 {
		t.Fatalf("missing --dir on web pull code=%d", code)
	}
}

func TestWebPullExistingTargetFailsBeforeAuthInJSONAndHumanMode(t *testing.T) {
	existing := t.TempDir()
	for _, jsonMode := range []bool{false, true} {
		t.Run(fmt.Sprint(jsonMode), func(t *testing.T) {
			t.Setenv("TIANA_API_ORIGIN", "http://127.0.0.1:1")
			args := []string{"web", "pull", "web-one", "--dir", existing}
			if jsonMode {
				args = append(args, "--json")
			}
			var out, diagnostics bytes.Buffer
			code := runCLI(context.Background(), args, nil, &out, &diagnostics)
			if code != 2 {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, &out, &diagnostics)
			}
			if jsonMode {
				var body struct {
					Status string `json:"status"`
					Error  struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(out.Bytes(), &body); err != nil {
					t.Fatalf("invalid JSON %s: %v", &out, err)
				}
				if body.Status != "failed" || body.Error.Code != "TARGET_ALREADY_EXISTS" {
					t.Fatalf("unexpected JSON: %s", &out)
				}
			} else if !strings.Contains(diagnostics.String(), "already exists") {
				t.Fatalf("unexpected stderr: %s", &diagnostics)
			}
		})
	}
}

func TestWriteAppResultFormatsPullResultHumanAndJSON(t *testing.T) {
	pulled := apppublish.PullResult{
		WebID:         "web-one",
		InstanceID:    "ins-1",
		Dir:           "/tmp/dest",
		ServingSHA256: strings.Repeat("a", 64),
		Entry:         "app.js",
		FilesCount:    3,
		TotalBytes:    1024,
		LooksBundled:  true,
	}
	var out, diagnostics bytes.Buffer
	if code := writeAppResult(apppublish.Success(pulled), false, &out, &diagnostics); code != 0 {
		t.Fatalf("human code=%d", code)
	}
	text := out.String()
	for _, want := range []string{"Web ID: web-one", "Directory: /tmp/dest", "Files: 3 (1024 bytes)", "Entry: app.js", "compiled/bundled"} {
		if !strings.Contains(text, want) {
			t.Fatalf("human output missing %q: %s", want, text)
		}
	}
	out.Reset()
	if code := writeAppResult(apppublish.Success(pulled), true, &out, &diagnostics); code != 0 {
		t.Fatalf("json code=%d", code)
	}
	var envelope struct {
		Status string                `json:"status"`
		Data   apppublish.PullResult `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil || envelope.Status != "succeeded" || envelope.Data != pulled {
		t.Fatalf("unexpected JSON: %s (%v)", &out, err)
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
			credentials := account.CredentialPath(t)
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
