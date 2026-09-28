package authclient

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func unsetOriginEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"TIANA_API_ORIGIN", "TIANA_MGR_ORIGIN", "TIANA_AUTH_ORIGIN", "TIANA_TOKEN", "TIANA_TOKEN_FILE"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResolveOriginReusesOnlySavedAccountWithoutChangingStore(t *testing.T) {
	for _, customPath := range []bool{false, true} {
		t.Run(map[bool]string{false: "default-path", true: "custom-path"}[customPath], func(t *testing.T) {
			unsetOriginEnvironment(t)
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("TIANA_CREDENTIALS_FILE", "")
			path, err := DefaultCredentialPath()
			if err != nil {
				t.Fatal(err)
			}
			if customPath {
				path = filepath.Join(t.TempDir(), "account.json")
				t.Setenv("TIANA_CREDENTIALS_FILE", path)
			}
			const origin = "https://mgr.example.test:9443"
			if err := NewFileStore(path, origin).Save(Credential{AccessToken: "account-secret", RefreshToken: "refresh-secret", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(path)
			got, err := ResolveOrigin(context.Background())
			if err != nil || got != origin {
				t.Fatalf("origin=%q err=%v", got, err)
			}
			token, err := ConnectionCredential(context.Background())
			defer clear(token)
			if err != nil || string(token) != "account-secret" {
				t.Fatalf("cannot resolve account credential: %v", err)
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("credential store was modified")
			}
		})
	}
}

func TestResolveOriginExplicitConfigurationNeverReadsSavedAccounts(t *testing.T) {
	unsetOriginEnvironment(t)
	t.Setenv("TIANA_CREDENTIALS_FILE", t.TempDir())
	t.Setenv("TIANA_API_ORIGIN", "https://mgr.example.test")
	if got, err := ResolveOrigin(context.Background()); err != nil || got != "https://mgr.example.test" {
		t.Fatalf("mgr precedence: %q %v", got, err)
	}
	t.Setenv("TIANA_API_ORIGIN", "invalid-origin")
	if got, err := ResolveOrigin(context.Background()); err != nil || got != "invalid-origin" {
		t.Fatalf("invalid explicit value was replaced: %q %v", got, err)
	}
}

func TestLegacyOriginVariablesIgnored(t *testing.T) {
	for _, legacy := range []string{"TIANA_MGR_ORIGIN", "TIANA_AUTH_ORIGIN"} {
		for _, mode := range []string{"missing", "empty", "explicit", "saved", "multiple"} {
			t.Run(legacy+"/"+mode, func(t *testing.T) {
				unsetOriginEnvironment(t)
				t.Setenv(legacy, "https://legacy.example.test")
				path := filepath.Join(t.TempDir(), "credentials.json")
				t.Setenv("TIANA_CREDENTIALS_FILE", path)
				const origin = "https://mgr.example.test"
				if mode == "explicit" || mode == "saved" || mode == "multiple" {
					if err := NewFileStore(path, origin).Save(Credential{AccessToken: "selected-secret", RefreshToken: "selected-refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "explicit" {
					t.Setenv("TIANA_API_ORIGIN", "  "+origin+"  ")
				}
				if mode == "empty" {
					t.Setenv("TIANA_API_ORIGIN", " ")
				}
				if mode == "multiple" {
					if err := NewFileStore(path, "https://legacy.example.test").Save(Credential{AccessToken: "legacy-secret", RefreshToken: "legacy-refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
						t.Fatal(err)
					}
				}
				got, err := ResolveOrigin(context.Background())
				token, credentialErr := ConnectionCredential(context.Background())
				defer clear(token)
				if mode == "explicit" || mode == "saved" {
					if err != nil || got != origin || credentialErr != nil || string(token) != "selected-secret" {
						t.Fatalf("selected wrong origin/account: origin=%q err=%v credentialErr=%v", got, err, credentialErr)
					}
				} else if err == nil || got != "" || credentialErr == nil {
					t.Fatalf("legacy origin unexpectedly enabled account selection: origin=%q err=%v credentialErr=%v", got, err, credentialErr)
				}
			})
		}
	}
}

func TestResolveOriginFailsClosed(t *testing.T) {
	for _, mode := range []string{"missing", "empty", "multiple", "corrupt", "http", "userinfo", "path", "query", "fragment", "public", "symlink", "directory", "oversized", "explicit-empty"} {
		t.Run(mode, func(t *testing.T) {
			unsetOriginEnvironment(t)
			path := filepath.Join(t.TempDir(), "credentials.json")
			t.Setenv("TIANA_CREDENTIALS_FILE", path)
			origin := "https://mgr.example.test"
			switch mode {
			case "http":
				origin = "http://mgr.example.test"
			case "userinfo":
				origin = "https://private-secret@mgr.example.test"
			case "path":
				origin += "/private-secret"
			case "query":
				origin += "?private-secret"
			case "fragment":
				origin += "#private-secret"
			}
			accounts := map[string]Credential{origin: {AccessToken: "private-secret", RefreshToken: "private-secret"}}
			if mode == "multiple" {
				accounts["https://second.example.test"] = accounts[origin]
			}
			if mode == "empty" {
				accounts = nil
			}
			contents, _ := json.Marshal(map[string]any{"credentials": accounts})
			if mode == "corrupt" {
				contents = []byte("private-secret")
			}
			if mode == "oversized" {
				contents = []byte(strings.Repeat("x", (8<<20)+1))
			}
			if mode != "missing" {
				if err := writeFixtureFile(path, contents, 0600); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "public":
				chmodFixtureFile(path, 0644)
			case "directory":
				t.Setenv("TIANA_CREDENTIALS_FILE", filepath.Dir(path))
			case "symlink":
				link := path + ".link"
				if err := os.Symlink(path, link); err != nil {
					if runtime.GOOS == "windows" {
						t.Skipf("symlinks need developer mode or privilege: %v", err)
					}
					t.Fatal(err)
				}
				t.Setenv("TIANA_CREDENTIALS_FILE", link)
			case "explicit-empty":
				t.Setenv("TIANA_API_ORIGIN", "")
			}
			got, err := ResolveOrigin(context.Background())
			if err == nil || got != "" {
				t.Fatalf("unexpected successful resolution: %q", got)
			}
			if strings.Contains(err.Error(), "private-secret") || strings.Contains(err.Error(), path) {
				t.Fatal("diagnostic leaked credential or path")
			}
			if mode == "multiple" && !strings.Contains(err.Error(), "multiple saved management origins") {
				t.Fatalf("missing selection guidance: %v", err)
			}
		})
	}
}
