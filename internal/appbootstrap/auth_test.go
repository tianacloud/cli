package appbootstrap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
			json.NewEncoder(w).Encode(map[string]any{"user": map[string]string{"user_id": "owner", "tenant_id": "tenant", "display_name": "Owner"}})
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
			var body struct {
				RequestID string `json:"request_id"`
				ExpiresAt int64  `json:"expires_at"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.RequestID == "" || body.RequestID != r.Header.Get("Idempotency-Key") || body.ExpiresAt > time.Now().Add(11*time.Minute).Unix() {
				t.Fatal("invalid preview scope request")
			}
			json.NewEncoder(w).Encode(map[string]any{"tenant_id": "tenant", "instance_id": instanceID, "endpoint_id": "ep-7k3rbz6104qg3qk2z6de5z2vmh", "token": "scoped-instance-only", "status": "ACTIVE", "sync_status": "complete", "expires_at": body.ExpiresAt})
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
	if connection.Token != "scoped-instance-only" || connection.Origin != "https://"+host+":9443" {
		t.Fatal("wrong scoped connection or port")
	}
	encoded, err := json.Marshal(connection)
	if err != nil {
		t.Fatal(err)
	}
	var descriptor map[string]any
	if json.Unmarshal(encoded, &descriptor) != nil || descriptor["sql_api"] != "hrana-v3" || descriptor["protocol"] != nil {
		t.Fatal("preview must select the parameterized Hrana SQL API without forcing the App HTTP socket")
	}
	if _, err = identity.Connection(context.Background()); err != nil || minted != 1 {
		t.Fatal("preview must reuse its in-memory scoped credential")
	}
	if strings.Contains(string(encoded), "account-token") {
		t.Fatal("account credential exposed to application")
	}
	unauthorized = true
	if _, err = identity.Connection(context.Background()); err == nil {
		t.Fatal("revoked ownership still exposes saved token")
	}
}
