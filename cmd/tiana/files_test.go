package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/fileclient"
)

func fileCredential(t *testing.T, origin, path string) {
	t.Helper()
	if err := authclient.NewFileStore(path, origin).Save(authclient.Credential{AccessToken: "access-files", RefreshToken: "refresh-files", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour), User: authclient.User{ID: "user-files", TenantID: "tenant-files"}}); err != nil {
		t.Fatal(err)
	}
}
func TestFilesListAndURL(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer access-files" {
			t.Error("missing account credential")
		}
		switch r.URL.Path {
		case "/api/v1/files/list":
			if r.URL.Query().Get("path") != "docs" {
				t.Error("wrong prefix")
			}
			json.NewEncoder(w).Encode(fileclient.Page{Items: []fileclient.Object{{Path: "docs/a.txt", Kind: "file", Size: 5}}, Cursor: "page2"})
		case "/api/v1/files/read-grant":
			json.NewEncoder(w).Encode(fileclient.Grant{Token: "secret-file-token", Origin: "https://files.example.test", Root: "/users/tenant-files/files/", ScopeID: "scope", ExpiresAt: time.Now().Add(time.Minute).Unix()})
		default:
			t.Error("unexpected request")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	fileCredential(t, server.URL, env.credentialsPath)
	var out, diag bytes.Buffer
	if code := runCLI(t.Context(), []string{"file", "list", "docs", "--json"}, nil, &out, &diag); code != 0 || !strings.Contains(out.String(), `"cursor":"page2"`) {
		t.Fatal(code, out.String(), diag.String())
	}
	out.Reset()
	diag.Reset()
	if code := runCLI(t.Context(), []string{"file", "get-url", "报告 #.txt"}, nil, &out, &diag); code != 0 || out.String() != "https://files.example.test/users/tenant-files/files/%E6%8A%A5%E5%91%8A%20%23.txt?_fs_scope=scope\n" {
		t.Fatal(code, out.String(), diag.String())
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}
func TestFilesPutRecoversCompletedReplacementWithoutDeletingOrUploadingAgain(t *testing.T) {
	prepares, uploads, completes := 0, 0, 0
	oss := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uploads++
		if r.Header.Get("Authorization") != "" {
			t.Error("account credential leaked")
		}
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(201)
	}))
	defer oss.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/files/uploads":
			prepares++
			var request fileclient.Prepare
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if !request.Overwrite || request.RequestID == "" {
				t.Error("missing replacement identity")
			}
			json.NewEncoder(w).Encode(fileclient.Upload{ID: "42", Path: "file.txt", Status: "uploading", Form: &fileclient.Form{URL: oss.URL, Fields: map[string]string{"Content-Type": "text/plain"}}})
		case "/api/v1/files/uploads/42/complete":
			completes++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(503)
			io.WriteString(w, `{"error":{"code":"UNAVAILABLE"}}`)
		case "/api/v1/files/uploads/42":
			json.NewEncoder(w).Encode(fileclient.Upload{ID: "42", Path: "file.txt", Status: "uploading", Completed: true})
		default:
			t.Error("unexpected request", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	fileCredential(t, server.URL, env.credentialsPath)
	source := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(source, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"file", "put", source, "file.txt", "--overwrite", "--json"}
	var out, diag bytes.Buffer
	if code := runCLI(t.Context(), args, nil, &out, &diag); code == 0 {
		t.Fatal("unknown completion reported success")
	}
	pending, err := authclient.NewFilePendingCommandStore(env.pendingPath).Load()
	if err != nil || pending.File.UploadID != "42" {
		t.Fatal("upload receipt not saved", err)
	}
	out.Reset()
	diag.Reset()
	if code := runCLI(t.Context(), args, nil, &out, &diag); code != 0 {
		t.Fatal(code, out.String(), diag.String())
	}
	if prepares != 1 || uploads != 1 || completes != 1 {
		t.Fatal("replacement replayed", prepares, uploads, completes)
	}
	if _, err = authclient.NewFilePendingCommandStore(env.pendingPath).Load(); err != authclient.ErrPendingNotFound {
		t.Fatal("pending receipt not cleared", err)
	}
}
func TestFilesDeleteRequiresConfirmationAndUsesStableRequest(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "DELETE" || r.URL.Query().Get("path") != "a.txt" {
			t.Error("wrong delete")
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["request_id"] == "" {
			t.Error("missing request ID")
		}
		io.WriteString(w, `{"deleted":true}`)
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	fileCredential(t, server.URL, env.credentialsPath)
	var out, diag bytes.Buffer
	if code := runCLI(t.Context(), []string{"file", "delete", "a.txt"}, strings.NewReader("y\n"), &out, &diag); code != 2 || requests != 0 {
		t.Fatal(code, requests)
	}
	out.Reset()
	diag.Reset()
	if code := runCLI(t.Context(), []string{"file", "delete", "a.txt", "-f", "--json"}, nil, &out, &diag); code != 0 || requests != 1 {
		t.Fatal(code, requests, out.String(), diag.String())
	}
}
