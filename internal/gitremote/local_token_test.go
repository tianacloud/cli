package gitremote

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

func localTokenEnvironment(t *testing.T) string {
	t.Helper()
	for _, key := range []string{"TIANA_TOKEN", "TIANA_TOKEN_FILE", "TIANA_CA_FILE", "TIANA_GATEWAY_ADDRESS", "TIANA_MGR_ORIGIN", "TIANA_AUTH_ORIGIN"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "instance-tokens.json")
	t.Setenv("TIANA_INSTANCE_TOKENS_FILE", path)
	return path
}

func TestLocalInstanceTokenConfiguration(t *testing.T) {
	for _, mode := range []string{"saved", "default-path", "expired", "missing", "other-endpoint", "other-origin", "ambiguous", "corrupt", "unsafe", "invalid-token"} {
		t.Run(mode, func(t *testing.T) {
			path := localTokenEnvironment(t)
			if mode == "default-path" {
				t.Setenv("XDG_CONFIG_HOME", t.TempDir())
				t.Setenv("TIANA_INSTANCE_TOKENS_FILE", "")
				var err error
				path, err = authclient.DefaultInstanceTokenPath()
				if err != nil {
					t.Fatal(err)
				}
			}
			repo := testRepo(t)
			secret := "tia_" + strings.Repeat("A", 43)
			credential := authclient.InstanceTokenCredential{TenantID: "tenant", InstanceID: "git-one", EndpointID: repo.ID(), TokenID: "one", Token: secret, ExpiresAt: -1}
			if mode == "expired" {
				credential.ExpiresAt = time.Now().Add(-time.Minute).Unix()
			}
			if mode == "other-endpoint" {
				credential.EndpointID = "ep-other"
			}
			if mode == "other-origin" {
				t.Setenv("TIANA_MGR_ORIGIN", "https://other.example.test")
			}
			if mode == "invalid-token" {
				credential.Token = "PRIVATE_INVALID_TOKEN\n"
			}
			store := authclient.NewFileInstanceTokenStore(path, "https://mgr.example.test")
			if mode != "missing" {
				if _, err := store.Save(credential); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "ambiguous":
				credential.TenantID = "other"
				if _, err := store.Save(credential); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(path, []byte("PRIVATE_INVALID_TOKEN"), 0600); err != nil {
					t.Fatal(err)
				}
			case "unsafe":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(path)
			c, err := configuration(repo)
			if c.token != nil {
				defer c.token.Destroy()
			}
			wantError := mode == "ambiguous" || mode == "corrupt" || mode == "unsafe" || mode == "invalid-token"
			if wantError {
				if err == nil || strings.Contains(err.Error(), "PRIVATE_INVALID_TOKEN") || strings.Contains(err.Error(), secret) {
					t.Fatalf("expected redacted error: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			} else if mode == "saved" || mode == "default-path" {
				if c.token == nil || !bytes.Equal(c.token.BytesForHandoff(), []byte(secret)) {
					t.Fatal("did not select saved Token")
				}
			} else if c.token != nil {
				t.Fatal("selected unusable or out-of-scope Token")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("lookup modified store")
			}
		})
	}
}

func TestExplicitGitTokenBypassesLocalStore(t *testing.T) {
	for _, mode := range []string{"env", "file", "both", "empty", "invalid", "bad-file"} {
		t.Run(mode, func(t *testing.T) {
			path := localTokenEnvironment(t)
			if err := os.WriteFile(path, []byte("corrupt PRIVATE_LOCAL_SECRET"), 0600); err != nil {
				t.Fatal(err)
			}
			secret := "tia_" + strings.Repeat("A", 43)
			if mode == "env" || mode == "both" {
				t.Setenv("TIANA_TOKEN", secret)
			}
			if mode == "empty" {
				t.Setenv("TIANA_TOKEN", "")
			}
			if mode == "invalid" {
				t.Setenv("TIANA_TOKEN", "PRIVATE_INVALID_TOKEN\nvalue")
			}
			if mode == "file" || mode == "both" {
				file := filepath.Join(t.TempDir(), "token")
				if err := os.WriteFile(file, []byte(secret+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("TIANA_TOKEN_FILE", file)
			}
			if mode == "bad-file" {
				t.Setenv("TIANA_TOKEN_FILE", filepath.Join(t.TempDir(), "missing"))
			}
			c, err := configuration(testRepo(t))
			if c.token != nil {
				defer c.token.Destroy()
			}
			if mode == "env" || mode == "file" {
				if err != nil || c.token == nil || !bytes.Equal(c.token.BytesForHandoff(), []byte(secret)) {
					t.Fatalf("explicit Token bypass failed: %v", err)
				}
			} else if err == nil || strings.Contains(err.Error(), "local InstanceToken") || strings.Contains(err.Error(), "PRIVATE_") {
				t.Fatalf("wrong explicit rejection: %v", err)
			}
		})
	}
}
