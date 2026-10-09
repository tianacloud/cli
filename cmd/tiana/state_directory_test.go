package main

import (
	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWebReceiptUsesConfiguredStateDirectory(t *testing.T) {
	home := t.TempDir()
	config := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("TIANA_API_ORIGIN", "https://mgr.example.test")
	t.Setenv("TIANA_PENDING_COMMAND_FILE", "")
	want := apppublish.PublicationReceipt{Origin: "https://mgr.example.test", TenantID: "tenant", UserID: "user", WebID: "web", InstanceID: "instance", PublishID: "original-upload", SHA256: strings.Repeat("a", 64), CreatedAt: time.Now()}
	if err := persistWebPublication(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(config, "tiana", "web-publish-receipts")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("receipt escaped configured directory: %v", err)
	}
	count := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		count++
		got, err := authclient.NewFilePendingCommandStore(filepath.Join(directory, entry.Name())).Load()
		if err != nil || got.IdempotencyKey != "original-upload" || got.Origin != want.Origin || got.UserID != "user" || got.TenantID != "tenant" {
			t.Fatalf("publication identity lost: %v", err)
		}
	}
	if count != 1 {
		t.Fatalf("receipt count=%d", count)
	}
}
