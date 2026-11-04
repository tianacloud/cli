//go:build linux || darwin

package authclient

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPendingStoreSharedDirectory(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		name := "current-owner"
		if foreign {
			name = "different-owner"
		}
		t.Run(name, func(t *testing.T) {
			if foreign && os.Geteuid() != 0 {
				t.Skip("chown fixture requires root")
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "pending.json")
			if err := os.Chmod(dir, 0777); err != nil {
				t.Fatal(err)
			}
			for name, body := range map[string]string{
				path:           `{"command":"db.create","idempotency_key":"test-key","origin":"https://mgr.example.test"}`,
				path + ".lock": "",
			} {
				if err := os.WriteFile(name, []byte(body), 0666); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(name, 0666); err != nil {
					t.Fatal(err)
				}
				if foreign {
					if err := os.Chown(name, 65534, 65534); err != nil {
						t.Fatal(err)
					}
				}
			}
			if foreign {
				if err := os.Chown(dir, 65534, 65534); err != nil {
					t.Fatal(err)
				}
			}
			store := NewFilePendingCommandStore(path)
			release, err := store.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			got, err := store.Load()
			if err != nil || got.IdempotencyKey != "test-key" {
				t.Fatalf("load: %v", err)
			}
			got.IdempotencyKey = "next-key"
			if err := store.Save(got); err != nil {
				t.Fatal(err)
			}
			if got, err := store.Load(); err != nil || got.IdempotencyKey != "next-key" {
				t.Fatalf("reload: %v", err)
			}
			if err := store.Delete(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
