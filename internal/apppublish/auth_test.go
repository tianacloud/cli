package apppublish

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

func TestProjectRequestRejectsAccountChange(t *testing.T) {
	called := false
	r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) { called = true })
	first, err := r.currentIdentity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	credential, loadErr := r.Client.LoadCredential()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	// Simulate the credential store being replaced by another successful login.
	store := authclient.NewFileStore(t.TempDir()+"/credentials.json", r.Client.Origin())
	credential.User.ID = "different-user"
	if err := store.Save(credential); err != nil {
		t.Fatal(err)
	}
	client, newErr := authclient.NewWithConfig(authclient.Config{Origin: r.Client.Origin(), Store: store})
	if newErr != nil {
		t.Fatal(newErr)
	}
	r.Client = client
	if _, err := r.request(t.Context(), first, "PUT", "/api/v1/web-projects/billing", map[string]string{"name": "Billing"}); err == nil || err.Code != "AUTH_REQUIRED" || called {
		t.Fatal("account changed during upload but write proceeded")
	}
}

func TestProjectUnauthorizedWriteDoesNotRefreshOrReplay(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		{"PUT", "/api/v1/web-projects/billing"},
		{"GET", "/api/v1/web-projects/billing/versions/version-a"},
		{"PUT", "/api/v1/web-projects/billing/versions/version-a"},
		{"POST", "/api/v1/web-projects/billing/versions/version-a/uploads"},
		{"POST", "/api/v1/web-projects/billing/versions/version-a/complete"},
		{"GET", "/api/v1/web-projects/billing/versions/version-a/bootstrap"},
	} {
		t.Run(route.method+route.path, func(t *testing.T) {
			calls, requestID := 0, ""
			r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) {
				calls++
				requestID = req.Header.Get("X-Request-ID")
				if req.URL.Path != route.path || req.Method != route.method {
					t.Error("unexpected request")
				}
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"code": "UNAUTHORIZED"})
			})
			id, err := r.currentIdentity(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			result, failure := r.request(t.Context(), id, route.method, route.path, nil)
			if calls != 1 || failure == nil || failure.Code != "AUTH_REQUIRED" || failure.ExitCode != 5 {
				t.Fatalf("calls=%d error=%v", calls, failure)
			}
			if requestID == "" || result.RequestID != requestID || failure.RequestID != requestID {
				t.Fatalf("request ID mismatch: wire=%q result=%q failure=%q", requestID, result.RequestID, failure.RequestID)
			}
		})
	}
}

func TestProjectRequestRefreshesExpiredCredential(t *testing.T) {
	requests := 0
	r, _, out, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) {
		requests++
		if req.URL.Path == "/api/v1/auth/refresh" {
			json.NewEncoder(w).Encode(map[string]any{"access_token": "fresh-access", "refresh_token": "fresh-refresh", "expires_in": 3600, "user": map[string]string{"user_id": "prn-test", "tenant_id": "tenant-a"}})
			return
		}
		if req.Header.Get("Authorization") != "Bearer fresh-access" {
			t.Error("did not use refreshed account credential")
		}
		json.NewEncoder(w).Encode(map[string]string{"project_id": "billing"})
	})
	credential, err := r.Client.LoadCredential()
	if err != nil {
		t.Fatal(err)
	}
	credential.ExpiresAt = time.Now().Add(-time.Minute)
	store := authclient.NewFileStore(t.TempDir()+"/credentials.json", r.Client.Origin())
	if err := store.Save(credential); err != nil {
		t.Fatal(err)
	}
	client, err := authclient.NewWithConfig(authclient.Config{Origin: r.Client.Origin(), Store: store, NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	r.Client = client
	if code := runTest(r, out, context.Background(), []string{"apps", "create", "--project", "billing", "--json"}); code != 0 || requests != 2 {
		t.Fatalf("refresh failed code=%d requests=%d output=%s", code, requests, out)
	}
}
