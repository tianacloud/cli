package authclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testAuthenticatedClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	store := NewFileStore(filepath.Join(t.TempDir(), "credentials.json"), server.URL)
	if err := store.Save(Credential{AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour), User: User{ID: "usr_test"}}); err != nil {
		t.Fatal(err)
	}
	return testClient(t, server, store, new(bytes.Buffer))
}

func TestResolveInstancePrefersIDThenExactName(t *testing.T) {
	var listCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/instances/inst_x":
			_, _ = io.WriteString(w, `{"id":"inst_x","display_name":"my-db","engine":"sqld","product_state":"ACTIVE"}`)
		case r.URL.Path == "/api/v1/instances/my-db":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"code":"INVALID_INSTANCE_ID","message":"instance_id is invalid"}}`)
		case r.URL.Path == "/api/v1/instances/dup":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"code":"INVALID_INSTANCE_ID","message":"instance_id is invalid"}}`)
		case r.URL.Path == "/api/v1/instances/missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"code":"INSTANCE_NOT_FOUND","message":"instance not found"}}`)
		case r.URL.Path == "/api/v1/instances/degraded":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"code":"CONTROL_UNAVAILABLE","message":"Control is unavailable","retryable":true}}`)
		case r.URL.Path == "/api/v1/instances":
			listCalls++
			switch r.URL.Query().Get("display_name") {
			case "my-db":
				_, _ = io.WriteString(w, `{"items":[{"id":"inst_x","display_name":"my-db","engine":"sqld","product_state":"ACTIVE"}],"page":1,"page_size":20,"total":1,"total_pages":1}`)
			case "dup":
				_, _ = io.WriteString(w, `{"items":[{"id":"inst_a","display_name":"dup"},{"id":"inst_b","display_name":"dup"}],"page":1,"page_size":20,"total":2,"total_pages":1}`)
			default:
				_, _ = io.WriteString(w, `{"items":[],"page":1,"page_size":20,"total":0,"total_pages":0}`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := testAuthenticatedClient(t, server)
	ctx := context.Background()

	instance, err := client.ResolveInstance(ctx, "inst_x")
	if err != nil || instance.ID != "inst_x" {
		t.Fatalf("ID resolution instance=%+v err=%v", instance, err)
	}
	instance, err = client.ResolveInstance(ctx, "my-db")
	if err != nil || instance.ID != "inst_x" {
		t.Fatalf("name resolution instance=%+v err=%v", instance, err)
	}
	if _, err = client.ResolveInstance(ctx, "dup"); err == nil {
		t.Fatal("duplicate name resolution unexpectedly succeeded")
	} else {
		var duplicate *DuplicateInstanceNameError
		if !errors.As(err, &duplicate) || duplicate.Count != 2 || len(duplicate.CandidateIDs) != 2 {
			t.Fatalf("duplicate error=%+v", err)
		}
	}
	if _, err = client.ResolveInstance(ctx, "missing"); !errors.Is(err, ErrInstanceNotFound) {
		t.Fatalf("missing resolution err=%v", err)
	}
	before := listCalls
	if _, err = client.ResolveInstance(ctx, "degraded"); err == nil {
		t.Fatal("degraded resolution unexpectedly succeeded")
	} else {
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != 503 {
			t.Fatalf("degraded resolution did not retain safe status: %v", err)
		}
	}
	if listCalls != before {
		t.Fatalf("service failure triggered a name query: %d -> %d", before, listCalls)
	}
}

func TestListInstancesSendsDisplayNameBeforePagination(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[],"page":2,"page_size":20,"total":0,"total_pages":0}`)
	}))
	defer server.Close()
	client := testAuthenticatedClient(t, server)
	if _, err := client.ListInstances(context.Background(), "  my-db  ", 2, 20); err != nil {
		t.Fatal(err)
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		t.Fatal(err)
	}
	if values.Get("display_name") != "my-db" || values.Get("page") != "2" || values.Get("page_size") != "20" {
		t.Fatalf("query=%q", query)
	}
}

func TestCreateInstanceTokenDistinguishesDeliveryFromReplay(t *testing.T) {
	var calls int
	mux := http.NewServeMux()
	// Mirror the production MGR route, independently of the client URL builder.
	mux.HandleFunc("POST /api/v1/instances/{instance_id}/endpoints/{endpoint_id}/tokens", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.PathValue("instance_id") != "inst_x" || r.PathValue("endpoint_id") != "ep-0abcdefghjkmnpqrstvwxyz012" || r.Header.Get("Idempotency-Key") != "" {
			t.Error("wrong Token target or legacy idempotency header")
		}
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"job_id":1,"sync_status":"complete","tenant_id":"ten","instance_id":"inst_x","endpoint_id":"ep-0abcdefghjkmnpqrstvwxyz012","token_id":"tok_0abcdefghjkmnpqrstvwxyz012","name":"cli","token":"tia_secret","expires_at":1789646400,"secret_recoverable":false}`)
			return
		}
		_, _ = io.WriteString(w, `{"job_id":1,"sync_status":"complete","tenant_id":"ten","instance_id":"inst_x","endpoint_id":"ep-0abcdefghjkmnpqrstvwxyz012","token_id":"tok_0abcdefghjkmnpqrstvwxyz012","expires_at":1789646400,"secret_recoverable":false}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := testAuthenticatedClient(t, server)
	request := CreateTokenRequest{RequestID: "cli-req-1", Name: "cli", ExpiresAt: 1789646400}

	delivered, err := client.CreateInstanceToken(context.Background(), "inst_x", "ep-0abcdefghjkmnpqrstvwxyz012", request)
	if err != nil {
		t.Fatal(err)
	}
	if !delivered.DeliveredSecret() || delivered.Token != "tia_secret" || delivered.TokenID == "" || delivered.TenantID != "ten" {
		t.Fatalf("delivered=%+v", delivered)
	}
	replay, err := client.CreateInstanceToken(context.Background(), "inst_x", "ep-0abcdefghjkmnpqrstvwxyz012", request)
	if err != nil {
		t.Fatal(err)
	}
	if replay.DeliveredSecret() || !replay.CommittedWithoutSecret() {
		t.Fatalf("replay=%+v", replay)
	}
}

func TestCreateInstanceTokenSurfacesUnknownCommit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"code":"COMMIT_STATUS_UNKNOWN","message":"lifecycle operation result is not yet authoritative","retryable":true,"operation_id":"op-unknown","command_not_after":"2026-09-10T12:00:30.000Z","secret_recoverable":false}}`)
	}))
	defer server.Close()
	client := testAuthenticatedClient(t, server)
	_, err := client.CreateInstanceToken(context.Background(), "inst_x", "ep-0abcdefghjkmnpqrstvwxyz012", CreateTokenRequest{RequestID: "cli-req-1", Name: "cli", ExpiresAt: InstanceTokenNoExpiry})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "COMMIT_STATUS_UNKNOWN" {
		t.Fatalf("err=%+v", err)
	}
}

func TestGetInstanceMapsNotFoundAndInvalidID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/instances/missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"code":"INSTANCE_NOT_FOUND","message":"instance not found"}}`)
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"code":"INVALID_INSTANCE_ID","message":"instance_id is invalid"}}`)
		}
	}))
	defer server.Close()
	client := testAuthenticatedClient(t, server)
	if _, err := client.GetInstance(context.Background(), "missing"); !errors.Is(err, ErrInstanceNotFound) {
		t.Fatalf("not-found err=%v", err)
	}
	if _, err := client.GetInstance(context.Background(), "not an id"); !errors.Is(err, ErrInstanceInvalidID) {
		t.Fatalf("invalid-id err=%v", err)
	}
}

func TestNonInteractiveRunAuthenticatedDoesNotStartLogin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/auth/transactions") {
			t.Errorf("non-interactive client started authentication: %s", r.URL.Path)
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	store := NewFileStore(filepath.Join(t.TempDir(), "credentials.json"), server.URL)
	client, err := NewWithConfig(Config{Origin: server.URL, HTTPClient: server.Client(), Store: store, Output: new(bytes.Buffer), NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	err = client.RunAuthenticated(context.Background(), func(context.Context, Credential) error {
		t.Fatal("operation ran without a credential")
		return nil
	})
	if !errors.Is(err, ErrAuthenticationRequired) {
		t.Fatalf("err=%v", err)
	}
}

func TestInstanceModelDecodesConnection(t *testing.T) {
	var instance Instance
	if err := json.Unmarshal([]byte(`{"id":"inst","connection":{"hostname":"ep-0abcdefghjkmnpqrstvwxyz012.db.example.test","url":"https://ep-0abcdefghjkmnpqrstvwxyz012.db.example.test"}}`), &instance); err != nil {
		t.Fatal(err)
	}
	if instance.Connection == nil || instance.Connection.URL == "" {
		t.Fatalf("instance=%+v", instance)
	}
}
