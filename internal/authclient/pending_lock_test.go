//go:build linux || darwin

package authclient

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPendingLockRejectsUnsafeFile(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "permissions", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pending.json")
			lock := path + ".lock"
			switch kind {
			case "symlink", "hardlink":
				target := path + ".target"
				if err := os.WriteFile(target, nil, 0600); err != nil {
					t.Fatal(err)
				}
				var err error
				if kind == "symlink" {
					err = os.Symlink(target, lock)
				} else {
					err = os.Link(target, lock)
				}
				if err != nil {
					t.Fatal(err)
				}
			case "permissions":
				if err := os.WriteFile(lock, nil, 0644); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := unix.Mkfifo(lock, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if release, err := NewFilePendingCommandStore(path).Acquire(context.Background()); err == nil {
				release()
				t.Fatal("unsafe lock accepted")
			}
		})
	}
}
