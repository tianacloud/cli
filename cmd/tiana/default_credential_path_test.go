package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

func TestDefaultCredentialPathSharedByManagementAndConnections(t *testing.T) {
	for _, legacyFile := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing-override", true: "existing-override"}[legacyFile], func(t *testing.T) {
			config := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", config)
			const origin = "https://mgr.example.test"
			t.Setenv("TIANA_API_ORIGIN", origin)
			for _, key := range []string{"TIANA_TOKEN", "TIANA_TOKEN_FILE"} {
				t.Setenv(key, "")
				if err := os.Unsetenv(key); err != nil {
					t.Fatal(err)
				}
			}
			credential := authclient.Credential{AccessToken: "default-account", RefreshToken: "fixture-refresh", ExpiresAt: time.Now().Add(time.Hour), User: authclient.User{ID: "owner", TenantID: "tenant"}}
			store, err := authclient.NewCredentialStore(origin)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Save(credential); err != nil {
				t.Fatal(err)
			}
			legacy := filepath.Join(t.TempDir(), "legacy.json")
			if legacyFile {
				other := credential
				other.AccessToken = "legacy-account"
				if err := authclient.NewFileStore(legacy, origin).Save(other); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(legacy)
			// An inherited obsolete variable must not redirect any credential reader.
			t.Setenv("TIANA_CREDENTIALS_FILE", legacy)
			var diagnostics bytes.Buffer
			client, err := newAuthClient(context.Background(), &diagnostics, true)
			if err != nil {
				t.Fatal(err)
			}
			value, err := client.ConnectionCredential(t.Context(), "owner", "tenant")
			if err != nil || string(value) != "default-account" {
				t.Errorf("management client did not use the default credential store: %v", err)
			}
			clear(value)
			value, err = authclient.ConnectionCredential(t.Context())
			if err != nil || string(value) != "default-account" {
				t.Fatalf("connection resolver did not use the default credential store: %v", err)
			}
			clear(value)
			after, _ := os.ReadFile(legacy)
			if !bytes.Equal(before, after) {
				t.Fatal("obsolete credential file changed")
			}
		})
	}
}
