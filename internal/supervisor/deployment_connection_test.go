package supervisor

import (
	"bytes"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDeploymentCAAndEndpointOptions(t *testing.T) {
	server := httptest.NewTLSServer(nil)
	defer server.Close()
	certificate := server.Certificate().Raw
	path := filepath.Join(t.TempDir(), "root.crt")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TIANA_CA_FILE", path)
	parsed, err := ParseCLI([]string{"connect", "--", "turso", "db", "shell", "https://ep-01j5c9m7q2v8x4k6n3r0t1w2yz.db.env123.tiana.test:18445", "SELECT 1"})
	if err != nil {
		t.Fatal(err)
	}
	trust, err := resolveGatewayTrust(parsed.Connect.Gateway)
	if err != nil {
		t.Fatal(err)
	}
	if trust.InsecureTLS || len(trust.RootCertDER) != 1 || !bytes.Equal(trust.RootCertDER[0], certificate) {
		t.Fatal("deployment CA not projected to helper trust")
	}
}
