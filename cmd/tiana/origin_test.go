package main

import (
	"bytes"
	"context"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/urfave/cli/v3"
)

func TestInheritedOriginReachesCommandsWithoutChangingEnvironmentOrNativeArguments(t *testing.T) {
	const origin = "https://configured.example.test"
	t.Setenv("TIANA_API_ORIGIN", origin)
	for _, command := range [][]string{{"login"}, {"status"}, {"sqlite", "shell"}, {"web", "serve"}, {"web", "upload"}, {"connect"}, {"git", "remote-helper"}} {
		root := newCLICommand(nil, io.Discard, io.Discard, nil)
		leaf := root
		for _, name := range command {
			leaf = leaf.Command(name)
		}
		wantArgs := []string{}
		if leaf.SkipFlagParsing {
			wantArgs = []string{"--", "native-client", "--config", "native-config.json"}
		}
		called := false
		leaf.Action = func(ctx context.Context, cmd *cli.Command) error {
			called = true
			got, err := authclient.ResolveOrigin(ctx)
			if err != nil || got != origin {
				t.Fatalf("%v origin=%q err=%v", command, got, err)
			}
			if len(cmd.Args().Slice()) != len(wantArgs) || (len(wantArgs) > 0 && !reflect.DeepEqual(cmd.Args().Slice(), wantArgs)) {
				t.Fatalf("%v changed native arguments: %v", command, cmd.Args().Slice())
			}
			return nil
		}
		args := append([]string{"tiana"}, command...)
		args = append(args, wantArgs...)
		if err := root.Run(context.Background(), args); err != nil || !called {
			t.Fatalf("%v dispatch error=%v called=%v", command, err, called)
		}
	}
	if got, err := authclient.ResolveOrigin(context.Background()); err != nil || got != origin || os.Getenv("TIANA_API_ORIGIN") != origin {
		t.Fatalf("invocation configuration escaped context: %q %v", got, err)
	}
}

func TestInheritedOriginSelectsAccountForActualStatus(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer access-configured" {
			t.Error("wrong method or configured account")
		}
		switch r.URL.Path {
		case "/api/v1/auth/transactions/whoami":
			io.WriteString(w, `{"user":{"user_id":"configured","email":"configured@example.test"}}`)
		case "/api/v1/usage":
			io.WriteString(w, testQuotaResponse)
		default:
			t.Error("unexpected request")
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "configured")
	saveTestCredential(t, "https://other.example.test", env.credentialsPath, "other")
	t.Setenv("TIANA_API_ORIGIN", server.URL)
	ca := filepath.Join(t.TempDir(), "root.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0644); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	if code := runCLI(context.Background(), []string{"--ca-file", ca, "status"}, nil, &out, &diag); code != 0 || !strings.Contains(out.String(), "configured@example.test") {
		t.Fatalf("code=%d diagnostics=%s", code, &diag)
	}
	if requests != 2 {
		t.Fatalf("requests=%d", requests)
	}
}

func TestRemovedConfigStopsBeforeCommand(t *testing.T) {
	t.Setenv("TIANA_API_ORIGIN", "https://mgr.example.test")
	for _, args := range [][]string{
		{"--config", "unused.json", "sqlite", "shell", "id"},
		{"sqlite", "shell", "id", "--config", "unused.json"},
		{"--config=unused.json", "status"},
		{"status", "--config", "unused.json"},
		{"--config", "unused.json", "connect", "--", "client"},
		{"--config", "unused.json", "git", "remote-helper", "origin", "tiana://ep.example.test/repo.git"},
	} {
		called := false
		var out, diag bytes.Buffer
		code := runCLIWithSQL(context.Background(), args, nil, &out, &diag, func(context.Context, sqliteOptions) int { called = true; return 0 })
		if code != 2 || called {
			t.Fatalf("removed config accepted: args=%v code=%d called=%v", args, code, called)
		}
	}
	var out, diag bytes.Buffer
	if code := runCLI(context.Background(), []string{"--help"}, nil, &out, &diag); code != 0 || strings.Contains(out.String(), "--config") {
		t.Fatalf("removed config in help: code=%d output=%s", code, &out)
	}
}
