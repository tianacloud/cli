package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/supervisor"
)

// Re-exec fixture exercises the same private helper dispatch as the CLI binary.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == supervisor.BuiltinHelperArgument {
		if err := supervisor.ServeBuiltinHelper(context.Background(), os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if filepath.Base(os.Args[0]) == "turso" && len(os.Args) >= 5 && os.Args[4] == "SELECT __builtin_native_fixture" {
		if os.Getenv("TIANA_TOKEN") != "" || strings.Contains(strings.Join(os.Args, " "), "tia_") {
			os.Exit(80)
		}
		if !strings.HasPrefix(os.Args[3], "http://127.0.0.1:") {
			os.Exit(81)
		}
		os.Exit(19)
	}
	os.Exit(m.Run())
}

func TestBuiltinConnectWithoutExternalHelper(t *testing.T) {
	for _, local := range []bool{false, true} {
		name := "explicit"
		if local {
			name = "local"
		}
		t.Run(name, func(t *testing.T) { testBuiltinConnectCredential(t, local) })
	}
}

func testBuiltinConnectCredential(t *testing.T, local bool) {
	t.Setenv("TIANA_TOKEN", "tia_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if local {
		if err := os.Unsetenv("TIANA_TOKEN"); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TIANA_MGR_ORIGIN", "")
		t.Setenv("TIANA_AUTH_ORIGIN", "")
		path := filepath.Join(t.TempDir(), "tokens.json")
		t.Setenv("TIANA_INSTANCE_TOKENS_FILE", path)
		_, err := authclient.NewFileInstanceTokenStore(path, "https://mgr.example.test").Save(authclient.InstanceTokenCredential{TenantID: "tenant", InstanceID: "sqlite-one", TokenID: "one", EndpointID: "ep-00000000000000000000000000", Token: "tia_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", ExpiresAt: -1})
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TIANA_CA_FILE", "")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(t.TempDir(), "turso")
	if err = os.Symlink(exe, native); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	args := []string{"connect", "--adapter", "sqld", "--allow-unisolated-loopback", "--", native, "db", "shell", "https://ep-00000000000000000000000000.db.example.test", "SELECT __builtin_native_fixture"}
	code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diag)
	if code != 19 || diag.Len() != 0 {
		t.Fatalf("code=%d diag=%s", code, &diag)
	}
}
func TestVerifyBuiltinInstall(t *testing.T) {
	var out, diag bytes.Buffer
	code := runCLI(context.Background(), []string{"verify-install"}, strings.NewReader(""), &out, &diag)
	if code != 0 || !strings.Contains(out.String(), "mode=builtin") || diag.Len() != 0 {
		t.Fatalf("code=%d out=%s diag=%s", code, &out, &diag)
	}
}
