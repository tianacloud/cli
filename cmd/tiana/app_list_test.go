package main

import (
	"bytes"
	"context"
	"encoding/json"
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

func appManagementFixture(t *testing.T, handler http.HandlerFunc) *authclient.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	dir := t.TempDir()
	creds := filepath.Join(dir, "credentials.json")
	t.Setenv("TIANA_API_ORIGIN", server.URL)
	t.Setenv("TIANA_CREDENTIALS_FILE", creds)
	t.Setenv("TIANA_PENDING_COMMAND_FILE", filepath.Join(dir, "pending.json"))
	store := authclient.NewFileStore(creds, server.URL)
	if e := store.Save(authclient.Credential{AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour), User: authclient.User{ID: "owner", TenantID: "tenant"}}); e != nil {
		t.Fatal(e)
	}
	c, e := authclient.NewWithConfig(authclient.Config{Origin: server.URL, Store: store, NonInteractive: true})
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func TestAppListAllPagesAndInteractiveQuit(t *testing.T) {
	calls := 0
	client := appManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/api/v1/apps" || r.Header.Get("Authorization") != "Bearer access" {
			t.Error("unexpected list request")
		}
		item := apppublish.App{AppID: "apps-a", Name: "First", OwnerID: "owner", TenantID: "tenant"}
		next := "apps-a"
		if r.URL.Query().Get("after") == "apps-a" {
			item.AppID = "apps-b"
			item.Name = "Second"
			next = ""
		}
		json.NewEncoder(w).Encode(apppublish.AppPage{Items: []apppublish.App{item}, NextCursor: next})
	})
	var out, diagnostics bytes.Buffer
	if code := runCLI(context.Background(), []string{"app", "list", "--json"}, nil, &out, &diagnostics); code != 0 {
		t.Fatalf("code=%d %s %s", code, &out, &diagnostics)
	}
	var result struct {
		Data apppublish.AppPage `json:"data"`
	}
	if json.Unmarshal(out.Bytes(), &result) != nil || len(result.Data.Items) != 2 || calls != 2 {
		t.Fatalf("list=%s calls=%d", &out, calls)
	}
	r, e := (apppublish.Runner{Client: client}).Bind(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	out.Reset()
	calls = 0
	if code := runAppList(t.Context(), r, false, true, strings.NewReader("q\n"), &out, &diagnostics); code != 0 || calls != 1 || strings.Contains(out.String(), "Second") {
		t.Fatalf("quit code=%d calls=%d out=%s", code, calls, &out)
	}
	out.Reset()
	calls = 0
	if code := runAppList(t.Context(), r, false, true, strings.NewReader("\n"), &out, &diagnostics); code != 0 || calls != 2 || !strings.Contains(out.String(), "Second") {
		t.Fatalf("next code=%d calls=%d out=%s", code, calls, &out)
	}
}
func TestAppListRejectsArgumentsBeforeNetwork(t *testing.T) {
	calls := 0
	appManagementFixture(t, func(w http.ResponseWriter, r *http.Request) { calls++ })
	var out, err bytes.Buffer
	if code := runCLI(t.Context(), []string{"app", "list", "extra"}, nil, &out, &err); code != 2 || calls != 0 {
		t.Fatalf("code=%d calls=%d", code, calls)
	}
}

type appPromptFailureWriter struct{ prompted bool }

func (w *appPromptFailureWriter) Write(p []byte) (int, error) {
	if w.prompted {
		return 0, io.ErrClosedPipe
	}
	w.prompted = strings.Contains(string(p), "-- More --")
	return len(p), nil
}
func TestAppListInteractiveNewlineOutputFailure(t *testing.T) {
	client := appManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"items":[{"app_id":"apps-a","owner_id":"owner","tenant_id":"tenant"}],"next_cursor":"apps-a"}`)
	})
	runner, e := (apppublish.Runner{Client: client}).Bind(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	var diagnostics bytes.Buffer
	if code := runAppList(t.Context(), runner, false, true, strings.NewReader("q\n"), &appPromptFailureWriter{}, &diagnostics); code != 1 {
		t.Fatalf("write failure code=%d", code)
	}
}
