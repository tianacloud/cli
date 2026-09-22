package gitremote

import (
	"crypto/x509"
	"github.com/tianacloud/cli/internal/clientconfig"
	"os"
	"path/filepath"
	"testing"
)

func TestGlobalTrustOverridesEnvironmentWithoutMutation(t *testing.T) {
	t.Setenv("TIANA_CA_FILE", "/missing/ca")
	t.Setenv("TIANA_INSTANCE_TOKENS_FILE", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("TIANA_TOKEN", "")
	os.Unsetenv("TIANA_TOKEN")
	t.Setenv("TIANA_TOKEN_FILE", "")
	os.Unsetenv("TIANA_TOKEN_FILE")
	roots := x509.NewCertPool()
	c, err := configurationWithTrust(testRepo(t), &clientconfig.Trust{Roots: roots})
	if err != nil || c.tls.RootCAs != roots || c.tls.InsecureSkipVerify {
		t.Fatal("global trust not applied", err)
	}
	if os.Getenv("TIANA_CA_FILE") != "/missing/ca" {
		t.Fatal("modified process environment")
	}
}
