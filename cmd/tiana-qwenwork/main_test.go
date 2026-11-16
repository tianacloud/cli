package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"
)

func TestSourceEnvironment(t *testing.T) {
	for _, env := range [][]string{nil, {"OTHER=x"}, {"TIANA_FROM="}, {"TIANA_FROM=workbuddy"}} {
		got := sourceEnvironment(env)
		want := slices.Clone(env)
		if len(env) == 0 || env[0] == "OTHER=x" {
			want = append(want, "TIANA_FROM=qwenwork")
		}
		if !slices.Equal(got, want) {
			t.Fatalf("got=%v want=%v", got, want)
		}
	}
}

func TestConnectorWithRealCore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("browser launcher fixture is POSIX")
	}
	root := t.TempDir()
	executable := filepath.Join(root, "bin", "test_platform", "tiana")
	core := filepath.Join(filepath.Dir(executable), "libexec", "tiana")
	if err := os.MkdirAll(filepath.Dir(core), 0700); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", core, "../tiana")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(root, ".qwenwork-plugin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".qwenwork-plugin", "plugin.json"), []byte(`{"version":"7.8.9"}`), 0600); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(root, "xdg-open")
	if runtime.GOOS == "darwin" {
		launcher = filepath.Join(root, "open")
	}
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("TIANA_FROM", "qwenwork")
	source := ""
	unavailable := false
	pending := false
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/transactions":
			var request struct {
				Client struct {
					Source string `json:"acquisition_source"`
				} `json:"client"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			source = request.Client.Source
			io.WriteString(w, `{"transaction_id":"test","client_secret":"secret","user_code":"ABCD-EFGH","verification_uri_complete":"https://example.test/a","expires_in":600,"poll_interval":1}`)
			select {
			case started <- struct{}{}:
			default:
			}
		case "/api/v1/auth/transactions/test/poll":
			if pending {
				io.WriteString(w, `{"status":"pending"}`)
			} else {
				io.WriteString(w, `{"status":"approved","authorization_code":"code"}`)
			}
		case "/api/v1/auth/token":
			io.WriteString(w, `{"access_token":"access","refresh_token":"refresh","expires_in":3600,"user":{"user_id":"user","tenant_id":"tenant"}}`)
		case "/api/v1/auth/transactions/whoami":
			if unavailable {
				w.WriteHeader(503)
				io.WriteString(w, `{}`)
			} else {
				io.WriteString(w, `{"user":{"user_id":"user","tenant_id":"tenant"}}`)
			}
		case "/api/v1/auth/transactions/logout":
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected API %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	t.Setenv("TIANA_API_ORIGIN", server.URL)
	call := func(args ...string) (int, string, string) {
		t.Helper()
		var out, diag bytes.Buffer
		code := run(t.Context(), executable, args, nil, &out, &diag)
		return code, out.String(), diag.String()
	}
	if c, o, e := call("--version"); c != 0 || o != "7.8.9\n" || e != "" {
		t.Fatalf("version: %d %s %s", c, o, e)
	}
	if c, o, e := call("version"); c != 0 || o == "7.8.9\n" {
		t.Fatalf("core version: %d %s %s", c, o, e)
	}
	if c, o, e := call("auth", "show", "--status"); c != 0 || o != "{\"status\":\"unauthorized\"}\n" || e != "" {
		t.Fatalf("before: %d %s %s", c, o, e)
	}
	if c, o, e := call("auth", "init"); c != 0 || o != "{\"status\":\"authorized\"}\n" || e != "" || source != "qwenwork" {
		t.Fatalf("login: %d %s %s source=%s", c, o, e, source)
	}
	<-started
	if c, o, e := call("auth", "show", "--status"); c != 0 || o != "{\"status\":\"authorized\"}\n" || e != "" {
		t.Fatalf("status: %d %s %s", c, o, e)
	}
	if c, o, e := call("identity", "whoami"); c != 0 || !bytes.Contains([]byte(o), []byte(`"user_id":"user"`)) || bytes.Contains([]byte(o+e), []byte("access")) {
		t.Fatalf("identity: %d %s %s", c, o, e)
	}
	unavailable = true
	if c, o, e := call("auth", "show", "--status"); c == 0 || o != "" || !json.Valid([]byte(e)) {
		t.Fatalf("network: %d %s %s", c, o, e)
	}
	unavailable = false
	if c, o, e := call("auth", "logout"); c != 0 {
		t.Fatalf("logout: %d %s %s", c, o, e)
	}
	pending = true
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var out, diag bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- run(ctx, executable, []string{"auth", "init"}, nil, &out, &diag) }()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("login did not start")
	}
	cancel()
	select {
	case c := <-done:
		if c != 130 || out.Len() != 0 || !json.Valid(diag.Bytes()) {
			t.Fatalf("cancel: %d %s %s", c, &out, &diag)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("child did not stop")
	}
	if err := os.WriteFile(filepath.Join(root, "tiana", "credentials.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if c, o, e := call("auth", "show", "--status"); c == 0 || o != "" || !json.Valid([]byte(e)) {
		t.Fatalf("storage: %d %s %s", c, o, e)
	}
}
