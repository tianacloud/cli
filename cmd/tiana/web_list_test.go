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
	creds := filepath.Join(dir, "tiana", "credentials.json")
	t.Setenv("TIANA_API_ORIGIN", server.URL)
	t.Setenv("XDG_CONFIG_HOME", dir)
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
func TestWebListAllPages(t *testing.T) {
	calls := 0
	client := webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/api/v1/instances" || r.URL.Query().Get("engine") != "web" || r.URL.Query().Get("page_size") != "20" || r.Header.Get("Authorization") != "Bearer access" {
			t.Error("unexpected list request")
		}
		item := apppublish.WebProject{ID: "web-a", Name: "First", Description: "中文\nforged\t\x1b[31m\u202e", OwnerID: "owner", TenantID: "tenant"}
		next := "web-a"
		if r.URL.Query().Get("page") == "2" {
			item.ID = "web-b"
			item.Name = "Second"
			item.Description = ""
			next = ""
		}
		item.Endpoint = "https://site.example.test/web/" + item.ID + "/"
		page := 1
		if next == "" {
			page = 2
		}
		writeWebInstancePage(t, w, page, 2, item)
	})
	var out, diagnostics bytes.Buffer
	if code := runCLI(context.Background(), []string{"web", "list", "--json"}, nil, &out, &diagnostics); code != 0 {
		t.Fatalf("code=%d %s %s", code, &out, &diagnostics)
	}
	var result struct {
		Data apppublish.WebProjectPage `json:"data"`
	}
	if json.Unmarshal(out.Bytes(), &result) != nil || len(result.Data.Items) != 2 || calls != 2 || result.Data.Items[1].Endpoint != "https://site.example.test/web/web-b/" || result.Data.Items[0].Description != "中文\nforged\t\x1b[31m\u202e" {
		t.Fatalf("list=%s calls=%d", &out, calls)
	}
	r, e := (apppublish.Runner{Client: client}).Bind(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	out.Reset()
	calls = 0
	if code := runWebList(t.Context(), r, false, &out, &diagnostics); code != 0 || calls != 2 || !strings.Contains(out.String(), "Second") || strings.Contains(out.String(), "-- More --") || strings.Count(out.String(), "ID ") != 1 {
		t.Fatalf("code=%d calls=%d out=%s", code, calls, &out)
	}
	assertListMessage(t, out.String())
	out.Reset()
	calls = 0
	if code := runCLI(t.Context(), []string{"web", "list"}, nil, &out, &diagnostics); code != 0 || calls != 2 || !strings.Contains(out.String(), "Second") {
		t.Fatalf("plain code=%d calls=%d out=%s", code, calls, &out)
	}
	assertListMessage(t, out.String())
}
func TestWebListRejectsArgumentsBeforeNetwork(t *testing.T) {
	calls := 0
	webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) { calls++ })
	var out, err bytes.Buffer
	if code := runCLI(t.Context(), []string{"web", "list", "extra"}, nil, &out, &err); code != 2 || calls != 0 {
		t.Fatalf("code=%d calls=%d", code, calls)
	}
}

func TestWebListOutputFailure(t *testing.T) {
	client := webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeWebInstancePage(t, w, 1, 1, apppublish.WebProject{ID: "web-a", Name: "First"})
	})
	runner, e := (apppublish.Runner{Client: client}).Bind(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	var diagnostics bytes.Buffer
	if code := runWebList(t.Context(), runner, false, failedReceiptWriter{}, &diagnostics); code != 1 {
		t.Fatalf("write failure code=%d", code)
	}
}

func writeWebInstancePage(t *testing.T, w http.ResponseWriter, page, totalPages int, project apppublish.WebProject) {
	t.Helper()
	err := json.NewEncoder(w).Encode(authclient.InstancePage{Page: page, PageSize: 20, Total: totalPages, TotalPages: totalPages, Items: []authclient.Instance{{ID: "instance-" + project.ID, DisplayName: project.Name, Notes: project.Description, Engine: "web", ProductRevision: "1", CreatedAt: "2026-10-05T00:00:00Z", Web: &authclient.WebInstanceMetadata{Endpoint: project.Endpoint, ID: project.ID}, Connection: &authclient.InstanceConnection{URL: project.Endpoint}}}})
	if err != nil {
		t.Fatal(err)
	}
}
