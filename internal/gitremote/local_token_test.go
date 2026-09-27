package gitremote

import (
	"context"
	"github.com/tianacloud/cli/internal/authclient"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGitUsesAccountInsteadOfLocalInstanceToken(t *testing.T) {
	for _, mode := range []string{"account", "multiple-accounts", "explicit", "file", "missing", "invalid-explicit"} {
		t.Run(mode, func(t *testing.T) {
			for _, key := range []string{"TIANA_TOKEN", "TIANA_TOKEN_FILE"} {
				t.Setenv(key, "")
				os.Unsetenv(key)
			}
			origin := "https://mgr.example.test"
			t.Setenv("TIANA_API_ORIGIN", origin)
			path := filepath.Join(t.TempDir(), "credentials.json")
			t.Setenv("TIANA_CREDENTIALS_FILE", path)
			legacy := filepath.Join(t.TempDir(), "instance-tokens.json")
			os.WriteFile(legacy, []byte("broken"), 0600)
			t.Setenv("TIANA_INSTANCE_TOKENS_FILE", legacy)
			want := "account-secret"
			if mode != "missing" {
				if err := authclient.NewFileStore(path, origin).Save(authclient.Credential{AccessToken: want, RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "explicit" {
				want = "explicit-secret"
				t.Setenv("TIANA_TOKEN", want)
			}
			if mode == "file" {
				want = "file-secret"
				p := filepath.Join(t.TempDir(), "token")
				os.WriteFile(p, []byte(want+"\n"), 0600)
				t.Setenv("TIANA_TOKEN_FILE", p)
			}
			if mode == "invalid-explicit" {
				t.Setenv("TIANA_TOKEN", "")
			}
			ctx := context.Background()
			if mode == "multiple-accounts" {
				if err := authclient.NewFileStore(path, "https://other.example.test").Save(authclient.Credential{AccessToken: "other-secret", RefreshToken: "other-refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := configurationWithContext(ctx, testRepo(t))
			if mode == "missing" || mode == "invalid-explicit" {
				if err == nil {
					t.Fatal("accepted missing credential")
				}
				return
			}
			if err != nil || cfg.token == nil || string(cfg.token.BytesForHandoff()) != want {
				t.Fatalf("err=%v", err)
			}
			if strings.Contains(cfg.token.String(), "secret") {
				t.Fatal("leaked token")
			}
			cfg.token.Destroy()
			data, _ := os.ReadFile(legacy)
			if string(data) != "broken" {
				t.Fatal("legacy cache changed")
			}
		})
	}
}
