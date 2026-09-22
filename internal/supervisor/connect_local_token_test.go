package supervisor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

func TestConnectLocalInstanceToken(t *testing.T) {
	for _, mode := range []string{"saved", "default-path", "explicit", "empty", "invalid", "missing", "expired", "other-endpoint", "other-origin", "ambiguous", "corrupt", "unsafe", "invalid-saved"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("TIANA_TOKEN", "")
			if err := os.Unsetenv("TIANA_TOKEN"); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TIANA_MGR_ORIGIN", "")
			t.Setenv("TIANA_AUTH_ORIGIN", "")
			path := filepath.Join(t.TempDir(), "instance-tokens.json")
			t.Setenv("TIANA_INSTANCE_TOKENS_FILE", path)
			if mode == "default-path" {
				t.Setenv("XDG_CONFIG_HOME", t.TempDir())
				t.Setenv("TIANA_INSTANCE_TOKENS_FILE", "")
				var err error
				path, err = authclient.DefaultInstanceTokenPath()
				if err != nil {
					t.Fatal(err)
				}
			}
			endpoint, err := ParseEndpoint("ep-00000000000000000000000000.db.example.test")
			if err != nil {
				t.Fatal(err)
			}
			secret := "tia_" + strings.Repeat("A", 43)
			credential := authclient.InstanceTokenCredential{TenantID: "tenant", InstanceID: "sqlite-one", EndpointID: endpoint.ID(), TokenID: "one", Token: secret, ExpiresAt: -1}
			if mode == "expired" {
				credential.ExpiresAt = time.Now().Add(-time.Minute).Unix()
			}
			if mode == "other-endpoint" {
				credential.EndpointID = "ep-other"
			}
			if mode == "other-origin" {
				t.Setenv("TIANA_MGR_ORIGIN", "https://other.example.test")
			}
			if mode == "invalid-saved" {
				credential.Token = "PRIVATE_BAD_TOKEN\n"
			}
			store := authclient.NewFileInstanceTokenStore(path, "https://mgr.example.test")
			if mode != "missing" {
				if _, err := store.Save(credential); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "explicit":
				secret = "tia_" + strings.Repeat("B", 42) + "A"
				t.Setenv("TIANA_TOKEN", secret)
				if err := os.WriteFile(path, []byte("PRIVATE_CORRUPT_STORE"), 0600); err != nil {
					t.Fatal(err)
				}
			case "empty":
				t.Setenv("TIANA_TOKEN", "")
			case "invalid":
				t.Setenv("TIANA_TOKEN", "PRIVATE_BAD_TOKEN\nvalue")
			case "ambiguous":
				credential.TenantID = "other"
				if _, err := store.Save(credential); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(path, []byte("PRIVATE_CORRUPT_STORE"), 0600); err != nil {
					t.Fatal(err)
				}
			case "unsafe":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(path)
			input := strings.NewReader("SELECT 1;\n")
			token, err := readConnectEndpointCredential(context.Background(), ConnectOptions{Credential: DefaultCredentialSource()}, input, endpoint)
			if token != nil {
				defer token.Destroy()
			}
			if mode == "saved" || mode == "default-path" || mode == "explicit" {
				if err != nil || token == nil || !bytes.Equal(token.BytesForHandoff(), []byte(secret)) {
					t.Fatalf("wrong local selection: %v", err)
				}
			} else if err == nil || strings.Contains(err.Error(), "PRIVATE_") || strings.Contains(err.Error(), secret) {
				t.Fatalf("expected redacted error: %v", err)
			}
			if mode == "ambiguous" || mode == "corrupt" || mode == "unsafe" || mode == "invalid-saved" {
				if strings.Contains(err.Error(), "connection Token is required") {
					t.Fatal("store error fell through to missing-credential handling")
				}
			}
			if input.Len() != len("SELECT 1;\n") {
				t.Fatal("consumed native stdin")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("modified store")
			}
		})
	}
}
