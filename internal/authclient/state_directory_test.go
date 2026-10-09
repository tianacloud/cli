package authclient

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPendingStateUsesWritableXDGDirectory(t *testing.T) {
	home := t.TempDir()
	config := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("TIANA_API_ORIGIN", "https://mgr.example.test")
	path, err := DefaultPendingCommandPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != filepath.Join(config, "tiana") {
		t.Fatalf("pending escaped XDG directory: %s", path)
	}
	store := NewFilePendingCommandStore(path)
	unlock, err := store.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	command := PendingCommand{Command: "sqlite.create", Origin: "https://mgr.example.test", IdempotencyKey: "original-request"}
	if err = store.Save(command); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Load()
	if err != nil || reopened.IdempotencyKey != "original-request" {
		t.Fatalf("cannot recover original identity: %v", err)
	}
	legacy, err := os.UserConfigDir()
	if err == nil && legacy != config {
		if _, err = os.Stat(filepath.Join(legacy, "tiana")); !os.IsNotExist(err) {
			t.Fatalf("legacy directory was written: %v", err)
		}
	}
}

func TestUnresolvedLegacyPendingBlocksNewDefault(t *testing.T) {
	home := t.TempDir()
	config := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("TIANA_API_ORIGIN", "https://mgr.example.test")
	path, err := DefaultPendingCommandPath()
	if err != nil {
		t.Fatal(err)
	}
	legacyRoot, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(legacyRoot, "tiana", filepath.Base(path))
	if filepath.Join(legacyRoot, "tiana") == filepath.Join(config, "tiana") {
		t.Skip("previous platform path already uses XDG")
	}
	data := []byte(`{"command":"sqlite.create","origin":"https://mgr.example.test","idempotency_key":"original-request"}`)
	if err = os.MkdirAll(filepath.Dir(old), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(old, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = DefaultPendingCommandPath(); err == nil {
		t.Fatal("unresolved legacy request became invisible")
	}
	after, err := os.ReadFile(old)
	if err != nil || string(after) != string(data) {
		t.Fatal("legacy identity changed")
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("new intent written despite unresolved original")
	}
}

func TestPendingFailureReportsLocalPath(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	store := NewFilePendingCommandStore(filepath.Join(blocked, "pending.json"))
	_, err := store.Acquire(t.Context())
	if err == nil || !strings.Contains(err.Error(), blocked) || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("local path/reason lost: %v", err)
	}
}
