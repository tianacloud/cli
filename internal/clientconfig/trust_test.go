package clientconfig

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTrustLimitsAndContextIsolation(t *testing.T) {
	server := httptest.NewTLSServer(nil)
	defer server.Close()
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	for _, tc := range []struct {
		name  string
		data  []byte
		valid bool
	}{
		{"one", cert, true}, {"eight", bytes.Repeat(cert, 8), true}, {"nine", bytes.Repeat(cert, 9), false}, {"empty", nil, false}, {"invalid", []byte("not a cert"), false}, {"oversized", make([]byte, 65537), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "root.crt")
			if err := os.WriteFile(path, tc.data, 0600); err != nil {
				t.Fatal(err)
			}
			trust, err := Load(path)
			if (err == nil) != tc.valid {
				t.Fatalf("unexpected load error %v", err)
			}
			if tc.valid {
				if trust.IsDefault {
					t.Fatal("explicit CA marked as default")
				}
				ctx := WithTrust(context.Background(), trust)
				if FromContext(ctx) != trust || FromContext(context.Background()) != nil {
					t.Fatal("trust leaked across invocations")
				}
			}
		})
	}
}

func TestEmbeddedStagingTrust(t *testing.T) {
	t.Setenv("TIANA_API_ORIGIN", "https://console.tianacloud-staging.net")
	t.Chdir(t.TempDir())
	trust, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !trust.IsDefault || len(trust.Certificates) != 1 {
		t.Fatal("missing default CA")
	}
	cert, err := x509.ParseCertificate(trust.Certificates[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: trust.Roots, CurrentTime: cert.NotBefore.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultTrustUsesSystemRoots(t *testing.T) {
	for _, origin := range []string{"", "https://console.tianacloud.com", "https://custom.example.test"} {
		t.Run(origin, func(t *testing.T) {
			t.Setenv("TIANA_API_ORIGIN", origin)
			trust, err := Load("")
			system, systemErr := x509.SystemCertPool()
			if systemErr != nil {
				t.Fatal(systemErr)
			}
			if err != nil || !trust.Roots.Equal(system) || len(trust.Certificates) != 0 {
				t.Fatalf("default trust differs from system: %v", err)
			}
		})
	}
}
