package appbootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

func TestPreviewLoginResolvesOnlyAuthorizedInstance(t *testing.T) {
	const instanceID = "ins_preview"
	const host = "ep-7k3rbz6104qg3qk2z6de5z2vmh.db.example.test"
	unauthorized := false
	minted := 0
	var peer *httptest.Server
	peer = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/transactions":
			json.NewEncoder(w).Encode(map[string]any{"transaction_id": "preview", "client_secret": "local-proof", "user_code": "TEST", "verification_uri_complete": peer.URL + "/authorize", "expires_in": 60, "poll_interval": 1})
		case "/api/v1/auth/transactions/preview/poll":
			json.NewEncoder(w).Encode(map[string]any{"status": "approved", "authorization_code": "one-use"})
		case "/api/v1/auth/token":
			json.NewEncoder(w).Encode(map[string]any{"access_token": "account-token", "refresh_token": "account-refresh", "expires_in": 3600, "token_type": "Bearer", "user": map[string]string{"user_id": "owner", "tenant_id": "tenant"}})
		case "/api/v1/auth/transactions/whoami":
			json.NewEncoder(w).Encode(map[string]any{"sync_status": "complete", "user": map[string]string{"user_id": "owner", "tenant_id": "tenant", "display_name": "Owner"}})
		case "/api/v1/instances/" + instanceID:
			if r.Header.Get("Authorization") != "Bearer account-token" {
				t.Error("instance lookup missing account auth")
			}
			if unauthorized {
				w.WriteHeader(403)
				json.NewEncoder(w).Encode(map[string]string{"code": "FORBIDDEN"})
				return
			}
			json.NewEncoder(w).Encode(authclient.Instance{ID: instanceID, Engine: "sqlite", EndpointID: "ep-7k3rbz6104qg3qk2z6de5z2vmh", Connection: &authclient.InstanceConnection{Hostname: host, URL: "https://" + host + ":9443"}})
		case "/api/v1/instances/" + instanceID + "/endpoints/ep-7k3rbz6104qg3qk2z6de5z2vmh/tokens":
			minted++

			t.Error("preview must not mint instance tokens")
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer peer.Close()
	start := NewConsoleLogin(authclient.Config{Origin: peer.URL, HTTPClient: peer.Client()}, instanceID)
	flow, err := start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(flow.VerificationURL, peer.URL) {
		t.Fatal("wrong Console URL")
	}
	identity, err := flow.Complete(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	connection, err := identity.Connection(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if connection.Token != "account-token" || connection.Origin != "https://"+host+":9443" {
		t.Fatal("wrong account connection or port")
	}
	encoded, err := json.Marshal(connection)
	if err != nil {
		t.Fatal(err)
	}
	var descriptor map[string]any
	if json.Unmarshal(encoded, &descriptor) != nil || descriptor["sql_api"] != "hrana-v3" || descriptor["protocol"] != nil {
		t.Fatal("preview must select the parameterized Hrana SQL API without forcing the App HTTP socket")
	}
	if _, err = identity.Connection(context.Background()); err != nil || minted != 0 {
		t.Fatal("preview must reuse its in-memory account credential")
	}
	if strings.Contains(string(encoded), "account-refresh") {
		t.Fatal("refresh credential exposed to application")
	}
	unauthorized = true
	if _, err = identity.Connection(context.Background()); err == nil {
		t.Fatal("revoked ownership still exposes saved token")
	}
}

// Refresh credentials are deliberately held by a browser-specific broker, not
// the CLI's on-disk account and not the application JavaScript.
func TestPreviewAccountRotation(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprintf("unknown=%v", unknown), func(t *testing.T) {
			const instanceID = "ins_preview"
			const host = "ep-7k3rbz6104qg3qk2z6de5z2vmh.db.example.test"
			var clockOffset atomic.Int64
			var rotations atomic.Int64
			var newPolls atomic.Int64
			started, release := make(chan struct{}), make(chan struct{})
			var peer *httptest.Server
			peer = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/auth/transactions":
					json.NewEncoder(w).Encode(map[string]any{"transaction_id": "preview", "client_secret": "proof", "user_code": "TEST", "verification_uri_complete": peer.URL + "/authorize", "expires_in": 60, "poll_interval": 1})
				case "/api/v1/auth/transactions/preview/poll":
					json.NewEncoder(w).Encode(map[string]any{"status": "approved", "authorization_code": "one-use"})
				case "/api/v1/auth/token":
					json.NewEncoder(w).Encode(map[string]any{"access_token": "old-access", "refresh_token": "old-refresh", "expires_in": 3600, "token_type": "Bearer", "user": map[string]string{"user_id": "owner", "tenant_id": "tenant"}})
				case "/api/v1/auth/refresh":
					if rotations.Add(1) == 1 {
						close(started)
					}
					var body map[string]string
					if json.NewDecoder(r.Body).Decode(&body) != nil || body["refresh_token"] != "old-refresh" {
						t.Error("incorrect refresh credential")
					}
					<-release
					if unknown {
						w.WriteHeader(503)
						json.NewEncoder(w).Encode(map[string]string{"error": "commit_status_unknown"})
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"access_token": "new-access", "refresh_token": "new-refresh", "expires_in": 3600, "token_type": "Bearer", "sync_status": "pending", "user": map[string]string{"user_id": "owner", "tenant_id": "tenant"}})
				case "/api/v1/auth/transactions/whoami":
					status := "complete"
					if r.Header.Get("Authorization") == "Bearer new-access" && newPolls.Add(1) == 1 {
						status = "pending"
					}
					json.NewEncoder(w).Encode(map[string]any{"sync_status": status, "user": map[string]string{"user_id": "owner", "tenant_id": "tenant"}})
				case "/api/v1/instances/" + instanceID:
					json.NewEncoder(w).Encode(authclient.Instance{ID: instanceID, Engine: "sqlite", Connection: &authclient.InstanceConnection{Hostname: host}})
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer peer.Close()
			cfg := authclient.Config{Origin: peer.URL, HTTPClient: peer.Client(), Now: func() time.Time { return time.Now().Add(time.Duration(clockOffset.Load())) }}
			flow, err := NewConsoleLogin(cfg, instanceID)(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			identity, err := flow.Complete(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			clockOffset.Store(int64(2 * time.Hour))
			ctx, cancel := context.WithCancel(context.Background())
			first := make(chan error, 1)
			go func() { _, err := identity.Connection(ctx); first <- err }()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("refresh not started")
			}
			cancel()
			if err := <-first; !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation: %v", err)
			}
			var wg sync.WaitGroup
			for i := 0; i < 10; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					connection, err := identity.Connection(context.Background())
					if unknown {
						if err == nil {
							t.Error("unknown rotation exposed credential")
						}
						return
					}
					if err != nil || connection.Token != "new-access" {
						t.Errorf("new access unavailable: %v", err)
					}
					data, _ := json.Marshal(connection)
					if strings.Contains(string(data), "refresh") {
						t.Error("refresh credential reached browser")
					}
				}()
			}
			close(release)
			wg.Wait()
			if rotations.Load() != 1 {
				t.Fatalf("rotated %d times", rotations.Load())
			}
			if unknown {
				if _, err := identity.Connection(context.Background()); err == nil {
					t.Fatal("uncertain refresh not latched")
				}
				if rotations.Load() != 1 {
					t.Fatal("old refresh replayed")
				}
			} else if newPolls.Load() < 2 {
				t.Fatal("pending grant not awaited")
			}
		})
	}
}

func TestPreviewReusesSavedAccountAndRejectsAccountSwitch(t *testing.T) {
	store := &previewCredentials{value: authclient.Credential{AccessToken: "saved-access", RefreshToken: "saved-refresh", ExpiresAt: time.Now().Add(time.Hour), User: authclient.User{ID: "owner", TenantID: "tenant"}}}
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer saved-access" {
			t.Error("wrong saved account")
		}
		switch r.URL.Path {
		case "/api/v1/auth/transactions/whoami":
			json.NewEncoder(w).Encode(map[string]any{"sync_status": "complete", "user": map[string]string{"user_id": "owner", "tenant_id": "tenant"}})
		case "/api/v1/instances/ins_preview":
			json.NewEncoder(w).Encode(authclient.Instance{ID: "ins_preview", Engine: "sqlite", Connection: &authclient.InstanceConnection{Hostname: "ep-00000000000000000000000000.db.example.test"}})
		default:
			t.Error("unexpected login/issuance", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer peer.Close()
	identity, err := NewAccountIdentity(t.Context(), authclient.Config{Origin: peer.URL, HTTPClient: peer.Client(), Store: store}, "ins_preview")
	if err != nil {
		t.Fatal(err)
	}
	got, err := identity.Connection(t.Context())
	if err != nil || got.Token != "saved-access" {
		t.Fatal("saved account not reused", err)
	}
	cred, _ := store.Load()
	cred.User.ID = "other"
	store.Save(cred)
	if _, err = identity.Connection(t.Context()); err == nil {
		t.Fatal("account change not rejected")
	}
	cred, _ = store.Load()
	if cred.RefreshToken != "saved-refresh" {
		t.Fatal("preview changed account refresh credential")
	}
}

func TestPreviewDoesNotReleaseConcurrentAccountReplacement(t *testing.T) {
	store := &previewCredentials{value: authclient.Credential{AccessToken: "old", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour), User: authclient.User{ID: "owner", TenantID: "tenant"}}}
	var replace atomic.Bool
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/transactions/whoami":
			if replace.Swap(false) {
				value, _ := store.Load()
				value.AccessToken = "replacement"
				store.Save(value)
			}
			json.NewEncoder(w).Encode(map[string]any{"sync_status": "complete", "user": map[string]string{"user_id": "owner", "tenant_id": "tenant"}})
		case "/api/v1/instances/ins_preview":
			json.NewEncoder(w).Encode(authclient.Instance{ID: "ins_preview", Engine: "sqlite", Connection: &authclient.InstanceConnection{Hostname: "ep-00000000000000000000000000.db.example.test"}})
		default:
			t.Error("unexpected request", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer peer.Close()
	identity, err := NewAccountIdentity(t.Context(), authclient.Config{Origin: peer.URL, HTTPClient: peer.Client(), Store: store}, "ins_preview")
	if err != nil {
		t.Fatal(err)
	}
	replace.Store(true)
	if connection, err := identity.Connection(t.Context()); err == nil || connection.Token != "" {
		t.Fatal("unverified replacement released")
	}
	connection, err := identity.Connection(t.Context())
	if err != nil || connection.Token != "replacement" {
		t.Fatal("verified replacement unavailable", err)
	}
}
