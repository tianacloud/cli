package webtemplate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

func TestClientFetchesCurrentCatalogAndDownloadsWithoutAccountCredential(t *testing.T) {
	data := zipFixture(t, map[string]string{"metadata.json": `{"name":"notes"}`, "README.md": "source"})
	digest := sha256.Sum256(data)
	lists := 0
	object := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("management credential leaked to object storage")
		}
		w.Write(data)
	}))
	defer object.Close()
	mgr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-session" {
			t.Error("account was not used for MGR")
		}
		switch r.URL.Path {
		case "/api/v1/web-templates":
			lists++
			json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]string{"name": "notes", "description": "current notes"}}, "next_cursor": "notes"})
		case "/api/v1/web-templates/notes/init":
			json.NewEncoder(w).Encode(map[string]any{"name": "notes", "zip_size": len(data), "zip_sha256": hex.EncodeToString(digest[:]), "download_url": object.URL + "/private.zip?signature=fixture", "expires_at": time.Now().Add(15 * time.Minute)})
		default:
			t.Errorf("unexpected MGR path: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer mgr.Close()
	store := authclient.NewFileStore(filepath.Join(t.TempDir(), "auth.json"), mgr.URL)
	if err := store.Save(authclient.Credential{AccessToken: "test-session", RefreshToken: "test-refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	auth, err := authclient.NewWithConfig(authclient.Config{Origin: mgr.URL, Store: store, HTTPClient: mgr.Client()})
	if err != nil {
		t.Fatal(err)
	}
	client := Client{Auth: auth, DownloadHTTP: object.Client()}
	for i := 0; i < 2; i++ {
		page, err := client.List(t.Context(), "")
		if err != nil || len(page.Items) != 1 || page.Items[0].Description != "current notes" || page.NextCursor != "notes" {
			t.Fatalf("list: %+v %v", page, err)
		}
	}
	if lists != 2 {
		t.Fatal("catalog was cached")
	}
	target := filepath.Join(t.TempDir(), "notes")
	result, err := client.Init(t.Context(), "notes", target)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "signature") || result.Directory != target {
		t.Fatalf("result leaked download link: %s", encoded)
	}
	if _, err = os.Stat(filepath.Join(target, "README.md")); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadFailureLeavesNoProjectOrSignedLink(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		size   int
		digest string
	}{
		{"expired signature", 403, 1, "bad"},
		{"wrong size", 200, 9, "bad"},
		{"wrong checksum", 200, 1, strings.Repeat("0", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			object := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); w.Write([]byte("x")) }))
			defer object.Close()
			mgr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"zip_size": tc.size, "zip_sha256": tc.digest, "download_url": object.URL + "/private.zip?secret=SIGNED_SECRET"})
			}))
			defer mgr.Close()
			store := authclient.NewFileStore(filepath.Join(t.TempDir(), "auth.json"), mgr.URL)
			if err := store.Save(authclient.Credential{AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			auth, err := authclient.NewWithConfig(authclient.Config{Origin: mgr.URL, Store: store, HTTPClient: mgr.Client()})
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "project")
			if _, err = (Client{Auth: auth, DownloadHTTP: object.Client()}).Init(t.Context(), "notes", target); err == nil || strings.Contains(err.Error(), "SIGNED_SECRET") {
				t.Fatalf("failure: %v", err)
			}
			if _, err = os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("partial project: %v", err)
			}
		})
	}
}
