package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		t.Setenv("TIANA_MGR_ORIGIN", "https://mgr.example.test")
		path := filepath.Join(t.TempDir(), "credentials.json")
		t.Setenv("TIANA_CREDENTIALS_FILE", path)
		if err := authclient.NewFileStore(path, "https://mgr.example.test").Save(authclient.Credential{AccessToken: "tia_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
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
	if local {
		t.Setenv("TIANA_MGR_ORIGIN", "https://other.example.test")
		args = append([]string{"--config", writeTestConfig(t, "https://mgr.example.test")}, args...)
	}
	code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diag)
	if code != 19 || diag.Len() != 0 {
		t.Fatalf("code=%d diag=%s", code, &diag)
	}
}
func TestVerifyInstallCommandRemoved(t *testing.T) {
	for _, args := range [][]string{{"verify-install"}, {"verify-install", "--help"}, {"help", "verify-install"}} {
		var out, diag bytes.Buffer
		code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diag)
		if code != 2 || out.Len() != 0 {
			t.Fatalf("removed command accepted: code=%d out=%s diag=%s", code, &out, &diag)
		}
	}
	var out, diag bytes.Buffer
	if code := runCLI(context.Background(), []string{"--help"}, strings.NewReader(""), &out, &diag); code != 0 || strings.Contains(out.String(), "verify-install") {
		t.Fatalf("removed command in help: code=%d out=%s", code, &out)
	}
}
