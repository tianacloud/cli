package authclient

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestPinCredentialKeepsExistingSnapshotAndCannotRefreshIntoSwitchedAccount(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/resource" || r.Header.Get("Authorization") != "Bearer original-token" {
			t.Errorf("request escaped original snapshot: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.WriteHeader(401)
		io.WriteString(w, `{"error":{"code":"UNAUTHORIZED"}}`)
	}))
	defer server.Close()
	store := NewFileStore(filepath.Join(t.TempDir(), "auth.json"), server.URL)
	if err := store.Save(Credential{AccessToken: "original-token", RefreshToken: "original-refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	client, err := NewWithConfig(Config{Origin: server.URL, Store: store, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := client.PinCredential(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(Credential{AccessToken: "other-space-token", RefreshToken: "other-refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	pinnedAgain, err := pinned.PinCredential(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pinnedAgain.DoJSON(t.Context(), "GET", "/resource", nil, nil, nil); err == nil {
		t.Fatal("401 was accepted")
	}
	if requests != 1 {
		t.Fatalf("credential refresh or retry occurred: %d requests", requests)
	}
}
