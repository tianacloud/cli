package main

import (
	"bytes"
	"context"
	"encoding/json"
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

func writeTestConfig(t *testing.T, origin string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	contents, _ := json.Marshal(map[string]string{"managementOrigin": origin})
	if err := os.WriteFile(path, contents, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConfigReachesCommandsWithoutChangingEnvironmentOrNativeArguments(t *testing.T) {
	const origin = "https://configured.example.test"
	const environmentOrigin = "https://environment.example.test"
	t.Setenv("TIANA_MGR_ORIGIN", environmentOrigin)
	path := writeTestConfig(t, origin)
	for _, command := range [][]string{{"login"}, {"status"}, {"sqlite", "shell"}, {"apps", "serve"}, {"apps", "upload"}, {"connect"}, {"git", "remote-helper"}} {
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
		args := append([]string{"tiana", "--config", path}, command...)
		args = append(args, wantArgs...)
		if err := root.Run(context.Background(), args); err != nil || !called {
			t.Fatalf("%v dispatch error=%v called=%v", command, err, called)
		}
	}
	if got, err := authclient.ResolveOrigin(context.Background()); err != nil || got != environmentOrigin || os.Getenv("TIANA_MGR_ORIGIN") != environmentOrigin {
		t.Fatalf("invocation configuration escaped context: %q %v", got, err)
	}
}

func TestConfigOverridesOtherAccountsForActualStatus(t *testing.T) {
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
	t.Setenv("TIANA_MGR_ORIGIN", "https://other.example.test")
	path := writeTestConfig(t, server.URL)
	ca := filepath.Join(t.TempDir(), "root.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--config", path, "status"}, {"status", "--config", path}} {
		var out, diag bytes.Buffer
		if code := runCLI(context.Background(), append([]string{"--ca-file", ca}, args...), nil, &out, &diag); code != 0 || !strings.Contains(out.String(), "configured@example.test") {
			t.Fatalf("code=%d diagnostics=%s", code, &diag)
		}
	}
	if requests != 4 {
		t.Fatalf("requests=%d", requests)
	}
}

func TestExplicitInvalidConfigStopsBeforeCommand(t *testing.T) {
	t.Setenv("TIANA_MGR_ORIGIN", "https://otherwise-valid.example.test")
	valid := writeTestConfig(t, "https://configured.example.test")
	invalid := writeTestConfig(t, "http://invalid.example.test")
	for _, args := range [][]string{
		{"--config", "", "sqlite", "shell", "id"},
		{"--config", valid + ".missing", "sqlite", "shell", "id"},
		{"--config", invalid, "sqlite", "shell", "id"},
		{"--config", valid, "sqlite", "shell", "id", "--config", valid},
	} {
		called := false
		var out, diag bytes.Buffer
		code := runCLIWithSQL(context.Background(), args, nil, &out, &diag, func(context.Context, sqliteOptions) int { called = true; return 0 })
		if code != 2 || called {
			t.Fatalf("invalid config accepted: code=%d called=%v", code, called)
		}
	}
}
