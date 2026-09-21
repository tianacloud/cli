//go:build linux || darwin

package authclient

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPendingLockSubprocess(t *testing.T) {
	if path := os.Getenv("TIANA_TEST_PENDING_LOCK"); path != "" {
		release, err := NewFilePendingCommandStore(path).Acquire(context.Background())
		if os.Getenv("TIANA_TEST_PENDING_BUSY") == "1" {
			if err == nil {
				release()
				t.Fatal("competing process acquired pending lock")
			}
			if !strings.Contains(err.Error(), "another CLI command") {
				t.Fatal(err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			release()
		}
		return
	}
	path := filepath.Join(t.TempDir(), "pending.json")
	store := NewFilePendingCommandStore(path)
	release, err := store.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	run := func(busy string) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestPendingLockSubprocess$")
		cmd.Env = append(os.Environ(), "TIANA_TEST_PENDING_LOCK="+path, "TIANA_TEST_PENDING_BUSY="+busy)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("subprocess: %v %s", err, out)
		}
	}
	run("1")
	release()
	run("0")
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatal("lock inode must survive release")
	}
}

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

func TestPendingLockHonorsCanceledContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if release, err := NewFilePendingCommandStore(path).Acquire(ctx); err != context.Canceled {
		if release != nil {
			release()
		}
		t.Fatalf("canceled acquire: %v", err)
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatal("canceled acquire changed filesystem")
	}
}
