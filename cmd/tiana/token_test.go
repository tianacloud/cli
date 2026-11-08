package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tianacloud/cli/internal/authclient"
)

func TestInstanceTokenCreateForBackend(t *testing.T) {
	const secret = "tia_1synthetic-instance-credential"
	var creates, stored int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-usr_cli" {
			t.Errorf("request did not use account login")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/instances/orders":
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error":{"code":"INSTANCE_NOT_FOUND"}}`)
		case "GET /api/v1/instances":
			if r.URL.Query().Get("display_name") != "orders" {
				t.Errorf("missing exact name filter: %s", r.URL.RawQuery)
			}
			fmt.Fprintf(w, `{"items":[%s],"total":1}`, instanceResponse(testInstanceID, "orders", testEndpointID))
		case "POST /api/v1/instances/inst_cli/tokens":
			creates++
			var body authclient.CreateInstanceTokenRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.RequestID != "backend-token-request" || body.Name != "Orders backend" || body.ExpiresAt != -1 {
				t.Errorf("request: %#v", body)
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"instance_id":"inst_cli","token_id":"tok_backend","name":"Orders backend","token":%q,"expires_at":-1,"status":"ACTIVE","sync_status":"complete","job_id":9007199254740993,"extra_server_field":true}`, secret)
		case "PUT /api/v1/secrets-store/secrets/backend/sqlite":
			stored++
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["value"] != secret {
				t.Error("secret store did not receive exact issued token")
			}
			io.WriteString(w, `{"key":"backend/sqlite","version":"1","changed":true}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
	var out, diag bytes.Buffer
	code := runCLI(t.Context(), []string{"token", "create", "orders", "--name", "Orders backend", "--request-id", "backend-token-request", "--json"}, strings.NewReader(""), &out, &diag)
	if code != 0 || strings.Contains(diag.String(), secret) {
		t.Fatalf("code=%d diagnostic=%s", code, &diag)
	}
	var result struct {
		Status string `json:"status"`
		Data   struct {
			Token      string `json:"token"`
			JobID      string `json:"job_id"`
			InstanceID string `json:"instance_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "succeeded" || result.Data.Token != secret || result.Data.JobID != "9007199254740993" || result.Data.InstanceID != testInstanceID {
		t.Fatal("incorrect token receipt")
	}
	out.Reset()
	diag.Reset()
	code = runCLI(t.Context(), []string{"secrets", "set", "backend/sqlite", "--stdin", "--json"}, strings.NewReader(result.Data.Token), &out, &diag)
	if code != 0 || creates != 1 || stored != 1 || strings.Contains(out.String()+diag.String(), secret) {
		t.Fatalf("secret handoff: code=%d creates=%d writes=%d", code, creates, stored)
	}
}

func TestInstanceTokenCreateIncompleteDelivery(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		status               int
		body                 string
		wantStatus, wantCode string
	}{
		{"synchronizing", 202, `{"instance_id":"inst_cli","token_id":"tok_pending","sync_status":"pending","job_id":42}`, "pending", "TOKEN_NOT_DELIVERED"},
		{"replayed", 201, `{"instance_id":"inst_cli","token_id":"tok_replayed","sync_status":"complete"}`, "failed", "TOKEN_NOT_DELIVERED"},
		{"sync failed", 202, `{"instance_id":"inst_cli","token_id":"tok_failed","sync_status":"failed"}`, "failed", "TOKEN_NOT_DELIVERED"},
		{"server failure", 503, `{"error":{"code":"UNAVAILABLE"}}`, "unknown", "TOKEN_OUTCOME_UNKNOWN"},
		{"lost response", 201, `{"token":"synthetic-truncated-secret",`, "unknown", "TOKEN_OUTCOME_UNKNOWN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			creates := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					io.WriteString(w, instanceResponse(testInstanceID, "orders", testEndpointID))
					return
				}
				creates++
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
			var out, diag bytes.Buffer
			code := runCLI(t.Context(), []string{"token", "create", testInstanceID, "--name", "backend", "--json"}, strings.NewReader(""), &out, &diag)
			if code != 4 || creates != 1 || strings.Contains(out.String()+diag.String(), "synthetic-truncated-secret") {
				t.Fatalf("code=%d creates=%d stdout=%s stderr=%s", code, creates, &out, &diag)
			}
			var result struct {
				Status string `json:"status"`
				Error  struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Status != tc.wantStatus || result.Error.Code != tc.wantCode {
				t.Fatalf("unexpected result: %s, %v", &out, err)
			}
		})
	}
}

func TestInstanceTokenCreateExpiry(t *testing.T) {
	const expiry = int64(4102444800)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			io.WriteString(w, instanceResponse(testInstanceID, "orders", testEndpointID))
			return
		}
		var body authclient.CreateInstanceTokenRequest
		json.NewDecoder(r.Body).Decode(&body)
		if body.ExpiresAt != expiry || body.RequestID == "" {
			t.Fatalf("request=%#v", body)
		}
		fmt.Fprintf(w, `{"instance_id":"inst_cli","token_id":"tok_backend","token":"synthetic-token","sync_status":"complete","expires_at":%d}`, expiry)
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
	var out, diag bytes.Buffer
	if code := runCLI(context.Background(), []string{"token", "create", testInstanceID, "--name", "backend", "--expires-at", fmt.Sprint(expiry)}, strings.NewReader(""), &out, &diag); code != 0 {
		t.Fatalf("code=%d %s", code, &diag)
	}
	if !strings.Contains(out.String(), fmt.Sprint(expiry)) {
		t.Fatalf("missing expiry in receipt: %s", &out)
	}
}

func TestInstanceTokenReceiptOutputFailure(t *testing.T) {
	creates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			io.WriteString(w, instanceResponse(testInstanceID, "orders", testEndpointID))
			return
		}
		creates++
		io.WriteString(w, `{"instance_id":"inst_cli","token_id":"tok_output_failure","token":"synthetic-private-token","sync_status":"complete"}`)
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
	var diag bytes.Buffer
	code := runCLI(t.Context(), []string{"token", "create", testInstanceID, "--name", "backend", "--json"}, strings.NewReader(""), failedReceiptWriter{}, &diag)
	if code != 1 || creates != 1 || !strings.Contains(diag.String(), "tok_output_failure") || strings.Contains(diag.String(), "synthetic-private-token") {
		t.Fatalf("code=%d creates=%d diagnostic=%s", code, creates, &diag)
	}
}
