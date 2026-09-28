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

func TestEmbeddedDefaultTrust(t *testing.T) {
	t.Chdir(t.TempDir())
	trust, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !trust.IsDefault || len(trust.Certificates) != 2 {
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
