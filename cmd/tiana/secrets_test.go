package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecretsManagementRoundTrip(t *testing.T) {
	const value = "  synthetic secret\nwith whitespace  "
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v1/secrets-store/init":
			w.WriteHeader(202)
			w.Write([]byte(`{"instance_id":"vault-demo","job_id":"42","state":"CREATING","created":true,"passphrase":"must-not-print"}`))
		case r.Method == "GET" && r.URL.Path == "/api/v1/secrets-store/secrets":
			if r.URL.Query().Get("keyword") != "db" {
				t.Error("missing keyword")
			}
			w.Write([]byte(`{"items":[{"key":"production/db","version":"3"}],"next_cursor":null}`))
		case r.Method == "PUT" && r.URL.Path == "/api/v1/secrets-store/secrets/production/db":
			var body struct {
				Value string `json:"value"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Value != value {
				t.Error("value changed")
			}
			w.Write([]byte(`{"key":"production/db","version":"4","changed":true,"value":"must-not-print"}`))
		case r.Method == "DELETE" && r.URL.Path == "/api/v1/secrets-store/secrets/production/db":
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_secrets")
	for _, args := range [][]string{{"init", "--json"}, {"list", "--keyword", "db", "--json"}, {"set", "--stdin", "production/db", "--json"}, {"remove", "--force", "production/db", "--json"}} {
		var out, diag bytes.Buffer
		code := runCLI(context.Background(), append([]string{"secrets"}, args...), strings.NewReader(value), &out, &diag)
		if code != 0 {
			t.Fatalf("%s code=%d out=%s diag=%s", args[0], code, out.String(), diag.String())
		}
		if strings.Contains(out.String()+diag.String(), value) || strings.Contains(out.String()+diag.String(), "must-not-print") {
			t.Fatal("secret material printed")
		}
	}
	if calls != 4 {
		t.Fatalf("calls=%d", calls)
	}
}
func TestSecretsFailedWriteDoesNotEchoPeerBodyOrRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"code":"INVALID_VALUE","message":"synthetic-sensitive-value"}}`))
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_secrets")
	for _, jsonMode := range []bool{false, true} {
		var out, diag bytes.Buffer
		args := []string{"secrets", "set", "--stdin", "key"}
		if jsonMode {
			args = append(args, "--json")
		}
		code := runCLI(context.Background(), args, strings.NewReader("synthetic-sensitive-value"), &out, &diag)
		if code == 0 || strings.Contains(out.String()+diag.String(), "synthetic-sensitive-value") {
			t.Fatalf("unexpected result %d %s %s", code, out.String(), diag.String())
		}
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}
