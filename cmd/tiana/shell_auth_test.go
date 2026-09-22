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
	"strings"
	"testing"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/clientconfig"
	"github.com/tianacloud/cli/internal/testutil/sqlitepeer"
)

func TestShellAutomaticTokenRequiresAccountInstanceAccess(t *testing.T) {
	for _, mode := range []string{"saved", "legacy-saved", "missing", "denied", "explicit", "invalid-explicit", "empty-explicit"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("TIANA_TOKEN", "")
			os.Unsetenv("TIANA_TOKEN")
			requests := 0
			config, dials := sqlitepeer.Gateway(t, func(io.Reader, io.Writer) { t.Error("SQL after refusal") }, true)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method == http.MethodGet && r.URL.Path == "/api/v1/instances/"+testInstanceID+"/branches/main" {
					io.WriteString(w, sqliteBranchResponse(testInstanceID, "main", "production", testEndpointID))
					return
				}

				if r.Method != "GET" || r.URL.Path != "/api/v1/instances/"+testInstanceID || r.Header.Get("Authorization") != "Bearer access-usr_auto" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
					return
				}
				if mode == "denied" {
					w.WriteHeader(403)
					io.WriteString(w, `{"error":{"code":"FORBIDDEN"}}`)
					return
				}
				io.WriteString(w, sqliteInstanceResponse())
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_auto")
			if mode != "missing" {
				_, err := authclient.NewFileInstanceTokenStore(env.tokensPath, server.URL).Save(authclient.InstanceTokenCredential{TenantID: "tenant", InstanceID: testInstanceID, EndpointID: testEndpointID, TokenID: "one", Token: "tia_" + strings.Repeat("A", 43), ExpiresAt: authclient.InstanceTokenNoExpiry})
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode == "legacy-saved" {
				contents, err := os.ReadFile(env.tokensPath)
				if err != nil {
					t.Fatal(err)
				}
				legacy := bytes.Replace(contents, []byte(`"expires_at": -1`), []byte(`"expires_at": "9999-12-31T23:59:59.999Z"`), 1)
				if bytes.Equal(contents, legacy) {
					t.Fatal("fixture did not become legacy format")
				}
				if err := os.WriteFile(env.tokensPath, legacy, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "explicit" {
				t.Setenv("TIANA_TOKEN", "tia_"+strings.Repeat("A", 43))
				os.WriteFile(env.tokensPath, []byte("corrupt"), 0600)
			}
			if mode == "invalid-explicit" {
				t.Setenv("TIANA_TOKEN", "SECRET_INVALID")
			}
			if mode == "empty-explicit" {
				t.Setenv("TIANA_TOKEN", "")
			}
			var out, diag bytes.Buffer
			status := runSQLiteWith(context.Background(), []string{"shell", testInstanceID, "-e", "SELECT 1"}, strings.NewReader(""), &out, &diag, func(ctx context.Context, ref string, ni bool) (authclient.Instance, error) {
				return resolveSQLite(ctx, ref, "", ni, &diag)
			}, &config)
			want := 3
			if mode == "missing" || mode == "invalid-explicit" || mode == "empty-explicit" {
				want = 2
			}
			if mode == "denied" {
				want = 1
			}
			if status != want || strings.Contains(diag.String(), "SECRET_INVALID") {
				t.Fatalf("status=%d diagnostics=%s", status, &diag)
			}
			if (mode == "saved" || mode == "legacy-saved" || mode == "explicit") != (dials.Load() == 1) {
				t.Fatalf("wrong dial count: %d", dials.Load())
			}
			if (mode == "invalid-explicit" || mode == "empty-explicit") && requests != 0 {
				t.Fatal("invalid explicit credential fell back")
			}
		})
	}
}

func TestGlobalCAReachesMGRAndSQLContext(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"user":{"user_id":"usr_ca","email":"ca@example.com"}}`)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "root.crt")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_ca")
	for _, args := range [][]string{{"--ca-file", path, "whoami"}, {"whoami", "--ca-file", path}} {
		var out, diag bytes.Buffer
		if code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diag); code != 0 || !strings.Contains(out.String(), "ca@example.com") {
			t.Fatalf("code=%d error=%s", code, &diag)
		}
	}
	for _, args := range [][]string{{"--ca-file", path, "sqlite", "shell", "id"}, {"sqlite", "--ca-file", path, "shell", "id"}, {"sqlite", "shell", "id", "--ca-file", path}, {"sqlite", "shell", "--endpoint", sqlitepeer.Endpoint, "--ca-file", path}} {
		var out, diag bytes.Buffer
		called := false
		code := runCLIWithSQL(context.Background(), args, strings.NewReader(""), &out, &diag, func(ctx context.Context, _ sqliteOptions) int {
			called = true
			trust := clientconfig.FromContext(ctx)
			if trust == nil || len(trust.Certificates) != 1 {
				t.Error("global CA missing")
			}
			return 0
		})
		if code != 0 || !called {
			t.Fatalf("code=%d error=%s", code, &diag)
		}
	}
	t.Setenv("TIANA_CA_FILE", path+".missing")
	var out, diag bytes.Buffer
	if code := runCLI(context.Background(), []string{"--ca-file", path, "whoami"}, strings.NewReader(""), &out, &diag); code != 0 {
		t.Fatalf("explicit CA did not override env: %s", &diag)
	}
	t.Setenv("TIANA_CA_FILE", path)
	out.Reset()
	diag.Reset()
	if code := runCLI(context.Background(), []string{"whoami"}, strings.NewReader(""), &out, &diag); code != 0 {
		t.Fatalf("CA env not applied to MGR: %s", &diag)
	}
	for _, args := range [][]string{{"--ca-file", path, "sqlite", "shell", "id", "--ca-file", path}, {"sqlite", "shell", "id", "--ca-file="}, {"sqlite", "shell", "id", "--ca-file", path + ".missing"}} {
		out.Reset()
		diag.Reset()
		called := false
		code := runCLIWithSQL(context.Background(), args, strings.NewReader(""), &out, &diag, func(context.Context, sqliteOptions) int { called = true; return 0 })
		if code != 2 || called {
			t.Fatalf("invalid global CA accepted: code=%d", code)
		}
	}
}

func TestConnectRemovedCredentialFlags(t *testing.T) {
	for _, flag := range []string{"--token-env", "--token-file", "--token-stdin", "--non-interactive"} {
		var out, diag bytes.Buffer
		if code := runCLI(context.Background(), []string{"connect", flag, "--", "turso"}, strings.NewReader(""), &out, &diag); code != 2 || !strings.Contains(diag.String(), "removed option") {
			t.Fatalf("flag %s: code=%d error=%s", flag, code, &diag)
		}
	}
	var out, diag bytes.Buffer
	if code := runCLI(context.Background(), []string{"connect", "--help"}, strings.NewReader(""), &out, &diag); code != 0 {
		t.Fatal(code)
	}
	for _, flag := range []string{"--token-env", "--token-file", "--token-stdin", "--non-interactive"} {
		if strings.Contains(out.String(), flag) {
			t.Fatalf("removed flag remains in help: %s", flag)
		}
	}
}

func TestShellUnifiedFlags(t *testing.T) {
	for _, args := range [][]string{{"shell", "id", "-f", "file.sql"}, {"shell", "id", "-e", "SELECT 1"}, {"shell", "id"}} {
		o, err := parseSQLite(args)
		if err != nil {
			t.Fatal(err)
		}
		if len(args) == 3 {
			t.Fatal("bad test")
		}
		if len(args) == 2 && o.command != "shell" {
			t.Fatal(o.command)
		}
		if len(args) == 4 && args[2] == "-f" && (o.command != "run" || o.file != "file.sql") {
			t.Fatal("file action changed")
		}
		if len(args) == 4 && args[2] == "-e" && (o.command != "exec" || o.sql != "SELECT 1") {
			t.Fatal("execute action changed")
		}
	}
	for _, args := range [][]string{{"exec", "id", "--sql", "SELECT 1"}, {"run", "id", "--file", "f"}, {"shell", "id", "-e", "SELECT 1", "-f", "f"}, {"shell", "id", "-e", ""}, {"shell", "id", "-f", ""}, {"shell", "id", "--token-env", "X"}, {"shell", "id", "--token-file", "f"}, {"shell", "id", "--token-stdin"}, {"shell", "id", "--non-interactive"}} {
		var out, diag bytes.Buffer
		if code := runSQLite(context.Background(), args, strings.NewReader(""), &out, &diag); code != 2 {
			t.Fatalf("accepted removed/invalid arguments: %v code=%d", args, code)
		}
	}
}
