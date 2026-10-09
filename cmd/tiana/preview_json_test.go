package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type previewResultWriter struct{ results chan []byte }

func (w previewResultWriter) Write(p []byte) (int, error) {
	w.results <- append([]byte(nil), p...)
	return len(p), nil
}

func TestWebServeJSONEmitsOneProtectedLaunchResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/transactions/whoami":
			io.WriteString(w, `{"user":{"user_id":"owner","tenant_id":"tenant"}}`)
		case "/api/v1/web-projects/WebPreview12":
			io.WriteString(w, `{"id":"WebPreview12","name":"Preview","instance_id":"web-instance","owner_id":"owner","tenant_id":"tenant","project_revision":1,"entry":"app.js"}`)
		case "/api/v1/instances/web-instance":
			io.WriteString(w, `{"id":"web-instance","engine":"web","product_state":"ACTIVE"}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "owner")
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "app.js"), []byte("export {};"), 0600); err != nil {
		t.Fatal(err)
	}
	reserved, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reserved.Addr().(*net.TCPAddr).Port
	reserved.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	results := make(chan []byte, 2)
	done := make(chan int, 1)
	go func() {
		done <- runCLI(ctx, []string{"web", "serve", "WebPreview12", "--dir", dist, "--port", strconv.Itoa(port), "--json"}, strings.NewReader(""), previewResultWriter{results}, io.Discard)
	}()
	var raw []byte
	select {
	case raw = <-results:
	case code := <-done:
		t.Fatalf("preview exited before startup: %d", code)
	case <-time.After(5 * time.Second):
		t.Fatal("preview startup timed out")
	}
	out := bytes.NewBuffer(raw)
	got := decodeManagementJSON(t, out)
	if got["status"] != "succeeded" {
		t.Fatalf("preview startup failed: %v", got["error"])
	}
	data := got["data"].(map[string]any)
	if got["status"] != "succeeded" || data["port"] != float64(port) || data["authorization"] != "cli" || !strings.Contains(data["launch_url"].(string), "#") {
		t.Fatal("missing protected launch result")
	}
	// Anonymous callers still cannot obtain the saved account or application code.
	request, _ := http.NewRequest("POST", data["preview_url"].(string)+"/_tiana/connection", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://127.0.0.1:"+strconv.Itoa(port))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode == 200 {
		t.Fatal("anonymous preview exposed account")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("preview did not stop")
	}
	select {
	case <-results:
		t.Fatal("stdout contained a second startup result")
	default:
	}
}
