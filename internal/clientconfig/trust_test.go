package clientconfig

import (
	"bytes"
	"context"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
				ctx := WithTrust(context.Background(), trust)
				if FromContext(ctx) != trust || FromContext(context.Background()) != nil {
					t.Fatal("trust leaked across invocations")
				}
			}
		})
	}
}
