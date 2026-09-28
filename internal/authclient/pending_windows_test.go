//go:build windows

package authclient

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsPendingLockRejectsUnsafeFiles(t *testing.T) {
	for _, kind := range []string{"public", "hardlink", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "中文 pending.json")
			store := NewFilePendingCommandStore(path)
			release, err := store.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			release()
			switch kind {
			case "public":
				if err := chmodFixtureFile(path+".lock", 0644); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path+".lock", path+".alias"); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(path+".lock", path+".target"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".target", path+".lock"); err != nil {
					t.Skipf("symlinks need developer mode or privilege: %v", err)
				}
			}
			if release, err := store.Acquire(context.Background()); err == nil {
				release()
				t.Fatal("accepted unsafe lock")
			}
		})
	}
}

func TestWindowsPendingReplacementKeepsPrivateACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "中文 pending.json")
	store := NewFilePendingCommandStore(path)
	release, err := store.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	command := PendingCommand{Command: "db.create", IdempotencyKey: "first", Origin: "https://mgr.example.test"}
	if err := store.Save(command); err != nil {
		t.Fatal(err)
	}
	command.IdempotencyKey = "second"
	if err := store.Save(command); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Load(); err != nil || got.IdempotencyKey != "second" {
		t.Fatalf("private replacement: %+v %v", got, err)
	}
	if err := store.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatal("deletion removed lock", err)
	}
}
