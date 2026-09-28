package authclient

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestConnectionCredentialSources(t *testing.T) {
	for _, mode := range []string{"account", "explicit", "file", "both", "empty", "bad-file", "missing", "corrupt", "expired", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			for _, key := range []string{"TIANA_TOKEN", "TIANA_TOKEN_FILE"} {
				t.Setenv(key, "")
				os.Unsetenv(key)
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "credentials.json")
			t.Setenv("TIANA_API_ORIGIN", "https://mgr.example.test")
			t.Setenv("TIANA_CREDENTIALS_FILE", path)
			legacy := filepath.Join(dir, "instance-tokens.json")
			writeFixtureFile(legacy, []byte("broken legacy cache"), 0600)
			t.Setenv("TIANA_INSTANCE_TOKENS_FILE", legacy)
			if mode != "missing" {
				if err := NewFileStore(path, DefaultOrigin()).Save(Credential{AccessToken: "account-secret", RefreshToken: "refresh-secret", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
			}
			want := "account-secret"
			switch mode {
			case "explicit":
				t.Setenv("TIANA_TOKEN", "explicit-secret")
				want = "explicit-secret"
				writeFixtureFile(path, []byte("broken"), 0600)
			case "file":
				p := filepath.Join(dir, "token.txt")
				writeFixtureFile(p, []byte("file-secret\r\n"), 0600)
				t.Setenv("TIANA_TOKEN_FILE", p)
				want = "file-secret"
				os.Remove(path)
			case "both":
				t.Setenv("TIANA_TOKEN", "explicit-secret")
				t.Setenv("TIANA_TOKEN_FILE", "missing")
			case "empty":
				t.Setenv("TIANA_TOKEN", "")
			case "bad-file":
				t.Setenv("TIANA_TOKEN_FILE", filepath.Join(dir, "missing"))
			case "corrupt":
				writeFixtureFile(path, []byte("broken"), 0600)
			case "expired":
				NewFileStore(path, DefaultOrigin()).Save(Credential{AccessToken: "account-secret", RefreshToken: "refresh-secret", ExpiresAt: time.Now().Add(-time.Hour)})
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			value, err := ConnectionCredential(ctx)
			success := mode == "account" || mode == "explicit" || mode == "file"
			if (err == nil) != success || (success && string(value) != want) {
				t.Fatalf("mode=%s error=%v", mode, err)
			}
			clear(value)
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("credential leak")
			}
			data, _ := os.ReadFile(legacy)
			if string(data) != "broken legacy cache" {
				t.Fatal("legacy cache touched")
			}
		})
	}
}

func TestConnectionCredentialFileSafety(t *testing.T) {
	for _, mode := range []string{"maximum", "oversized", "symlink", "directory", "public", "newline", "carriage-return", "header-injection"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("TIANA_TOKEN", "")
			os.Unsetenv("TIANA_TOKEN")
			dir := t.TempDir()
			path := filepath.Join(dir, "token")
			value := strings.Repeat("x", MaxConnectionCredential)
			switch mode {
			case "oversized":
				value += "x"
			case "newline":
				value = "private-secret\n\n"
			case "carriage-return":
				value = "private-secret\r"
			case "header-injection":
				value = "private-secret\r\nInjected: true"
			}
			if err := writeFixtureFile(path, []byte(value), 0600); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "symlink":
				link := filepath.Join(dir, "link")
				if err := os.Symlink(path, link); err != nil {
					if runtime.GOOS == "windows" {
						t.Skipf("symlinks need developer mode or privilege: %v", err)
					}
					t.Fatal(err)
				}
				path = link
			case "directory":
				path = dir
			case "public":
				if err := chmodFixtureFile(path, 0644); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("TIANA_TOKEN_FILE", path)
			got, err := ConnectionCredential(context.Background())
			defer clear(got)
			if (err == nil) != (mode == "maximum") {
				t.Fatalf("unexpected result for %s: %v", mode, err)
			}
			if err != nil && (strings.Contains(err.Error(), "private-secret") || strings.Contains(err.Error(), path)) {
				t.Fatal("credential or path leaked")
			}
		})
	}
}

func TestConnectionCredentialDoesNotCrossOriginsOrWriteAccount(t *testing.T) {
	for _, key := range []string{"TIANA_TOKEN", "TIANA_TOKEN_FILE"} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
	path := filepath.Join(t.TempDir(), "credentials.json")
	first := "https://first.example.test"
	if err := NewFileStore(path, first).Save(Credential{AccessToken: "account-secret", RefreshToken: "refresh-secret", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TIANA_CREDENTIALS_FILE", path)
	t.Setenv("TIANA_API_ORIGIN", "https://second.example.test")
	if value, err := ConnectionCredential(context.Background()); err == nil || value != nil {
		t.Fatal("used another origin's account")
	}
	t.Setenv("TIANA_API_ORIGIN", first)
	value, err := ConnectionCredential(context.Background())
	defer clear(value)
	if err != nil || string(value) != "account-secret" {
		t.Fatal("cannot read current origin's account")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("account credentials modified")
	}
}
