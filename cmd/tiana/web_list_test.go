package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
)

func webManagementFixture(t *testing.T, handler http.HandlerFunc) *authclient.Client {
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
func TestWebListAllPagesAndInteractiveQuit(t *testing.T) {
	calls := 0
	client := webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/api/v1/web-projects" || r.Header.Get("Authorization") != "Bearer access" {
			t.Error("unexpected list request")
		}
		item := apppublish.Web{ID: "web-a", Name: "First", OwnerID: "owner", TenantID: "tenant"}
		next := "web-a"
		if r.URL.Query().Get("after") == "web-a" {
			item.ID = "web-b"
			item.Name = "Second"
			next = ""
		}
		json.NewEncoder(w).Encode(apppublish.WebPage{Items: []apppublish.Web{item}, NextCursor: next})
	})
	var out, diagnostics bytes.Buffer
	if code := runCLI(context.Background(), []string{"web", "list", "--json"}, nil, &out, &diagnostics); code != 0 {
		t.Fatalf("code=%d %s %s", code, &out, &diagnostics)
	}
	var result struct {
		Data apppublish.WebPage `json:"data"`
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
	if code := runWebList(t.Context(), r, false, true, strings.NewReader("q\n"), &out, &diagnostics); code != 0 || calls != 1 || strings.Contains(out.String(), "Second") {
		t.Fatalf("quit code=%d calls=%d out=%s", code, calls, &out)
	}
	out.Reset()
	calls = 0
	if code := runWebList(t.Context(), r, false, true, strings.NewReader("\n"), &out, &diagnostics); code != 0 || calls != 2 || !strings.Contains(out.String(), "Second") {
		t.Fatalf("next code=%d calls=%d out=%s", code, calls, &out)
	}
}
func TestWebListRejectsArgumentsBeforeNetwork(t *testing.T) {
	calls := 0
	webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) { calls++ })
	var out, err bytes.Buffer
	if code := runCLI(t.Context(), []string{"web", "list", "extra"}, nil, &out, &err); code != 2 || calls != 0 {
		t.Fatalf("code=%d calls=%d", code, calls)
	}
}
