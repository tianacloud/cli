package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/tianacloud/cli/internal/authclient"
)

func TestSQLiteTokensRemoved(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.NotFound(w, r) }))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	for _, path := range []string{env.pendingPath, env.tokensPath} {
		if err := os.WriteFile(path, []byte("unchanged"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"sqlite", "tokens"}, {"sqlite", "tokens", "create", "demo"}, {"sqlite", "token", "create", "demo"}} {
		var out, diag bytes.Buffer
		if code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diag); code != 2 {
			t.Fatalf("args=%v code=%d", args, code)
		}
	}
	var out, diag bytes.Buffer
	if code := runCLI(context.Background(), []string{"sqlite", "--help"}, strings.NewReader(""), &out, &diag); code != 0 || strings.Contains(out.String(), "tokens") {
		t.Fatalf("help=%s diag=%s", &out, &diag)
	}
	if calls != 0 {
		t.Fatal("removed command made requests")
	}
	for _, path := range []string{env.pendingPath, env.tokensPath} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "unchanged" {
			t.Fatal("local state changed")
		}
	}
}

func TestUnsupportedPendingOperationIsNotOverwritten(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected request"); io.WriteString(w, `{}`) }))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
	p := authclient.PendingCommand{Command: "unknown.operation", Args: []string{"unknown"}, Origin: server.URL, UserID: "usr_cli", IdempotencyKey: "old"}
	if err := authclient.NewFilePendingCommandStore(env.pendingPath).Save(p); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(env.pendingPath)
	var out, diag bytes.Buffer
	if code := runCLI(context.Background(), []string{"sqlite", "create", "new"}, strings.NewReader(""), &out, &diag); code != 1 {
		t.Fatalf("code=%d", code)
	}
	after, _ := os.ReadFile(env.pendingPath)
	if !bytes.Equal(before, after) || !strings.Contains(diag.String(), "not supported by this CLI") || strings.Contains(diag.String(), "tiana sqlite 'tokens'") {
		t.Fatalf("diagnostics=%s", &diag)
	}
}
