package authclient

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/tianacloud/cli/internal/buildconfig"
)

func TestBuildInsecureTLSAcceptsUntrustedMGRCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"transaction_id":"at_tls","client_secret":"secret","user_code":"H7K9-M2PX","verification_uri_complete":"https://auth.example/api/v1/a/H7K9-M2PX","expires_in":600,"poll_interval":0}`)
	}))
	defer server.Close()

	previous := buildconfig.InsecureTLS
	buildconfig.InsecureTLS = "true"
	defer func() { buildconfig.InsecureTLS = previous }()

	store := NewFileStore(filepath.Join(t.TempDir(), "credentials.json"), server.URL)
	client, err := NewWithConfig(Config{Origin: server.URL, Store: store, Output: new(bytes.Buffer)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateAuthTransaction(context.Background()); err != nil {
		t.Fatalf("insecure build rejected the untrusted certificate: %v", err)
	}
}

func TestDefaultBuildRejectsUntrustedMGRCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"transaction_id":"at_tls"}`)
	}))
	defer server.Close()

	previous := buildconfig.InsecureTLS
	buildconfig.InsecureTLS = "false"
	defer func() { buildconfig.InsecureTLS = previous }()

	store := NewFileStore(filepath.Join(t.TempDir(), "credentials.json"), server.URL)
	client, err := NewWithConfig(Config{Origin: server.URL, Store: store, Output: new(bytes.Buffer)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateAuthTransaction(context.Background()); err == nil {
		t.Fatal("default build accepted an untrusted certificate")
	}
}
