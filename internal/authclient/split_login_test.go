package authclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSplitLoginSurvivesProcessAndExchangesOnlyOnce(t *testing.T) {
	starts, polls, exchanges := 0, 0, 0
	approved := false
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/transactions":
			starts++
			json.NewEncoder(w).Encode(map[string]any{"transaction_id": "split", "client_secret": "synthetic-secret", "user_code": "TEST", "verification_uri_complete": "https://console.example.test/approve", "expires_in": 600, "poll_interval": 1})
		case "/api/v1/auth/transactions/split/poll":
			polls++
			if approved {
				json.NewEncoder(w).Encode(map[string]string{"status": "approved", "authorization_code": "synthetic-code"})
			} else {
				json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
			}
		case "/api/v1/auth/token":
			exchanges++
			json.NewEncoder(w).Encode(map[string]any{"access_token": "synthetic-access", "refresh_token": "synthetic-refresh", "expires_in": 3600, "user": map[string]string{"user_id": "owner", "tenant_id": "tenant"}})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	defer peer.Close()
	store := NewFileStore(filepath.Join(t.TempDir(), "credentials.json"), peer.URL)
	client, err := NewWithConfig(Config{Origin: peer.URL, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := client.StartLogin(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if starts != 1 || polls != 0 {
		t.Fatal("start must return and reuse the link without polling")
	}
	client, _ = NewWithConfig(Config{Origin: peer.URL, Store: store})
	if _, err = client.ResumeLogin(context.Background()); !errors.Is(err, ErrAuthorizationPending) {
		t.Fatal(err)
	}
	approved = true
	if _, err = client.ResumeLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Load()
	if err != nil || saved.AccessToken != "synthetic-access" {
		t.Fatal("missing account credential")
	}
	if _, err = client.ResumeLogin(context.Background()); !errors.Is(err, ErrLoginNotStarted) {
		t.Fatal("completed login reused")
	}
	if starts != 1 || polls != 2 || exchanges != 1 {
		t.Fatal("unexpected remote replay")
	}
}

func TestSplitLoginRejectsUnsafeStateBeforeNetwork(t *testing.T) {
	for _, kind := range []string{"permissions", "symlink", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			peer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unsafe state reached network") }))
			defer peer.Close()
			client, _ := NewWithConfig(Config{Origin: peer.URL, Store: NewFileStore(filepath.Join(t.TempDir(), "credentials.json"), peer.URL)})
			path, _ := client.pendingLoginPath()
			switch kind {
			case "permissions":
				if err := writeFixtureFile(path, []byte("{}"), 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(t.TempDir(), "target")
				if err := writeFixtureFile(target, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					if runtime.GOOS == "windows" {
						t.Skipf("symlinks need developer mode or privilege: %v", err)
					}
					t.Fatal(err)
				}
			case "oversized":
				if err := writeFixtureFile(path, make([]byte, maxPendingBytes+1), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := client.StartLogin(t.Context()); err == nil {
				t.Fatal("unsafe pending accepted")
			}
			if _, err := client.ResumeLogin(t.Context()); err == nil {
				t.Fatal("unsafe pending resumed")
			}
		})
	}
}

func TestLoginExchangeDoesNotFollowRedirect(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("authorization secret redirected") }))
	defer other.Close()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer peer.Close()
	client, _ := NewWithConfig(Config{Origin: peer.URL, Store: NewFileStore(filepath.Join(t.TempDir(), "credentials.json"), peer.URL)})
	if _, err := client.exchangeAuthorizationCode(t.Context(), AuthTransaction{ID: "test", ClientSecret: "synthetic"}, "code"); err == nil {
		t.Fatal("redirect accepted")
	}
}
