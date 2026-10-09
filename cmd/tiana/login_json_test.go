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
)

type loginJSONResult struct {
	Status string         `json:"status"`
	Data   map[string]any `json:"data"`
	Error  struct {
		Code, Message string
		NextAction    string `json:"next_action"`
	} `json:"error"`
}

func readLoginJSON(t *testing.T, output *bytes.Buffer) loginJSONResult {
	t.Helper()
	var result loginJSONResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	return result
}

func TestLoginJSONLocalFailureHasActionableCause(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++; t.Error("local failure sent auth request") }))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	blocked := filepath.Join(filepath.Dir(env.credentialsPath), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("fixture"), 0600); err != nil { // parent may not exist yet
		if err = os.MkdirAll(filepath.Dir(blocked), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(blocked, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", blocked)
	var out, diag bytes.Buffer
	code := runCLI(t.Context(), []string{"login", "--start", "--no-open", "--json"}, strings.NewReader(""), &out, &diag)
	got := readLoginJSON(t, &out)
	if code != 1 || got.Error.Code != "LOCAL_STATE_UNAVAILABLE" || !strings.Contains(got.Error.Message, blocked) || !strings.Contains(got.Error.NextAction, "XDG_CONFIG_HOME") {
		t.Fatalf("misclassified local failure: exit=%d result=%+v", code, got)
	}
	if _, exists := got.Data["logged_in"]; exists {
		t.Fatal("failed local operation invented login state")
	}
	if calls != 0 {
		t.Fatal("local failure sent remote request")
	}
}

func TestLoginResumeNoPendingPreservesExistingAccount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("resume without pending must not start authorization")
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "owner")
	before, err := os.ReadFile(env.credentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	code := runCLI(t.Context(), []string{"login", "--resume", "--json"}, strings.NewReader(""), &out, &diag)
	got := readLoginJSON(t, &out)
	if code != 1 || got.Error.Code != "NO_PENDING_AUTH" || got.Data["pending_auth"] != false {
		t.Fatalf("missing pending confused with login: exit=%d result=%+v", code, got)
	}
	if _, exists := got.Data["logged_in"]; exists {
		t.Fatal("resume inferred login state from absent pending")
	}
	after, err := os.ReadFile(env.credentialsPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("existing account modified")
	}
}

func TestLoginJSONSeparatesPendingExpiredAndDenied(t *testing.T) {
	for _, tc := range []struct {
		poll, want string
		exit       int
	}{{"pending", "AUTH_PENDING", 3}, {"expired", "AUTH_EXPIRED", 5}, {"denied", "AUTH_DENIED", 5}} {
		t.Run(tc.poll, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/auth/transactions":
					ioBody := `{"transaction_id":"fixture","client_secret":"synthetic-secret","user_code":"TEST","verification_uri_complete":"https://console.example.test/approve","expires_in":600,"poll_interval":1}`
					w.Write([]byte(ioBody))
				case "/api/v1/auth/transactions/fixture/poll":
					json.NewEncoder(w).Encode(map[string]string{"status": tc.poll})
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "owner")
			var out, diag bytes.Buffer
			if code := runCLI(t.Context(), []string{"login", "--start", "--no-open", "--json"}, strings.NewReader(""), &out, &diag); code != 3 {
				t.Fatalf("start failed: %d", code)
			}
			started := readLoginJSON(t, &out)
			if started.Data["pending_auth"] != true {
				t.Error("start did not identify pending authorization")
			}
			if _, exists := started.Data["logged_in"]; exists {
				t.Error("start claimed an unverified account state")
			}
			out.Reset()
			diag.Reset()
			code := runCLI(t.Context(), []string{"login", "--resume", "--json"}, strings.NewReader(""), &out, &diag)
			got := readLoginJSON(t, &out)
			if code != tc.exit || got.Error.Code != tc.want {
				t.Errorf("error classification: exit=%d result=%+v", code, got)
			}
			if strings.Contains(out.String()+diag.String(), "synthetic-secret") {
				t.Fatal("authorization secret disclosed")
			}
			if tc.poll == "denied" && got.Error.NextAction != "" {
				t.Fatal("denial prescribed another automatic authorization")
			}
		})
	}
}

func TestLoginJSONAccountSaveFailureKeepsResumableAuthorization(t *testing.T) {
	config := t.TempDir()
	credentials := filepath.Join(config, "tiana", "credentials.json")
	polls, exchanges := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/transactions":
			io.WriteString(w, `{"transaction_id":"fixture","client_secret":"synthetic-secret","user_code":"TEST","verification_uri_complete":"https://console.example.test/approve","expires_in":600,"poll_interval":1}`)
		case "/api/v1/auth/transactions/fixture/poll":
			polls++
			io.WriteString(w, `{"status":"approved","authorization_code":"synthetic-code"}`)
		case "/api/v1/auth/token":
			exchanges++
			if err := os.Mkdir(credentials, 0700); err != nil {
				t.Error(err)
			}
			io.WriteString(w, `{"access_token":"synthetic-access","refresh_token":"synthetic-refresh","expires_in":3600,"user":{"user_id":"owner","tenant_id":"tenant"}}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	t.Setenv("TIANA_API_ORIGIN", server.URL)
	t.Setenv("XDG_CONFIG_HOME", config)
	var out, diag bytes.Buffer
	if code := runCLI(t.Context(), []string{"login", "--start", "--json"}, strings.NewReader(""), &out, &diag); code != 3 {
		t.Fatal("start failed")
	}
	out.Reset()
	code := runCLI(t.Context(), []string{"login", "--resume", "--json"}, strings.NewReader(""), &out, &diag)
	got := readLoginJSON(t, &out)
	if code != 1 || got.Error.Code != "LOCAL_STATE_UNAVAILABLE" {
		t.Fatalf("save failure misclassified: %d", code)
	}
	if pending, known := got.Data["pending_auth"]; known && pending == false {
		t.Error("resumable authorization reported absent")
	}
	if err := os.Remove(credentials); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diag.Reset()
	if code = runCLI(t.Context(), []string{"login", "--resume", "--json"}, strings.NewReader(""), &out, &diag); code != 0 {
		t.Fatalf("repaired authorization did not resume: %d", code)
	}
	saved := readLoginJSON(t, &out)
	if saved.Data["logged_in"] != true || saved.Data["pending_auth"] != false || polls != 1 || exchanges != 1 {
		t.Fatal("saved authorization was lost or exchanged again")
	}
}
