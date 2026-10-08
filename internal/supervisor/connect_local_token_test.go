package supervisor

import (
	"context"
	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/testutil/account"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConnectUsesAccountWithoutTokenPrompt(t *testing.T) {
	for _, signedIn := range []bool{true, false} {
		t.Run(map[bool]string{true: "account", false: "signed-out"}[signedIn], func(t *testing.T) {
			for _, key := range []string{"TIANA_TOKEN", "TIANA_TOKEN_FILE"} {
				t.Setenv(key, "")
				os.Unsetenv(key)
			}
			origin := "https://mgr.example.test"
			t.Setenv("TIANA_API_ORIGIN", origin)
			path := account.CredentialPath(t)
			legacy := filepath.Join(t.TempDir(), "instance-tokens.json")
			os.WriteFile(legacy, []byte("broken"), 0600)
			t.Setenv("TIANA_INSTANCE_TOKENS_FILE", legacy)
			if signedIn {
				if err := authclient.NewFileStore(path, origin).Save(authclient.Credential{AccessToken: "account-secret", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
			}
			input := strings.NewReader("SELECT 1;\n")
			endpoint, _ := ParseEndpoint("ep-00000000000000000000000000.db.example.test")
			token, err := readConnectEndpointCredential(context.Background(), ConnectOptions{Credential: DefaultCredentialSource(), Interactive: true}, input, endpoint)
			if signedIn {
				if err != nil || token == nil || string(token.BytesForHandoff()) != "account-secret" {
					t.Fatalf("err=%v", err)
				}
				token.Destroy()
			} else if err == nil || !strings.Contains(err.Error(), "login") {
				t.Fatalf("expected login error %v", err)
			}
			if input.Len() != len("SELECT 1;\n") {
				t.Fatal("consumed native stdin")
			}
			data, _ := os.ReadFile(legacy)
			if string(data) != "broken" {
				t.Fatal("legacy cache changed")
			}
		})
	}
}
