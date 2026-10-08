package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentCreationRefusesSharedPendingStoreBeforeRequests(t *testing.T) {
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.URL.Path == "/api/v1/app-types" {
			if n == 1 {
				close(entered)
				<-release
			}
			_, _ = io.WriteString(w, appTypesResponse("sqlite", "git"))
			return
		}
		http.Error(w, `{"error":{"code":"UNAVAILABLE"}}`, http.StatusServiceUnavailable)
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_concurrent")
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		runCLI(context.Background(), []string{"git", "create", "first"}, strings.NewReader(""), io.Discard, io.Discard)
	}()
	defer func() { close(release); <-firstDone }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first command did not reach service")
	}
	var out, diag bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	status := runCLI(ctx, []string{"sqlite", "create", "second"}, strings.NewReader(""), &out, &diag)
	if status != 1 || calls.Load() != 1 || !strings.Contains(diag.String(), "another CLI command") {
		t.Fatalf("parallel mutation not blocked locally: status=%d calls=%d diagnostics=%s", status, calls.Load(), diag.String())
	}
}
