//go:build linux || darwin

package authclient

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPendingStoreRejectsUnsafeFiles(t *testing.T) {
	valid := []byte(`{"command":"db.create","idempotency_key":"test-key","origin":"https://mgr.example.test"}`)
	for _, kind := range []string{"permissions", "symlink", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pending.json")
			switch kind {
			case "permissions":
				if err := os.WriteFile(path, valid, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := path + ".target"
				if err := os.WriteFile(target, valid, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				data := append(append([]byte(nil), valid...), bytes.Repeat([]byte(" "), 8<<20)...)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := NewFilePendingCommandStore(path).Load(); err == nil {
				t.Fatal("unsafe pending file accepted")
			}
		})
	}
}

func TestPendingStoreRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.pipe")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	// Keep a nonblocking writer open: an unsafe reader would block waiting for data.
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if _, err := NewFilePendingCommandStore(path).Load(); err == nil {
		t.Fatal("FIFO accepted")
	}
}

func TestPendingStoreOversizedSavePreservesRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	store := NewFilePendingCommandStore(path)
	initial := PendingCommand{Command: "db.create", IdempotencyKey: "key", Origin: "https://mgr.example.test"}
	if err := store.Save(initial); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	next := initial
	next.Args = []string{strings.Repeat("x", 8<<20)}
	if err := store.Save(next); err == nil {
		t.Fatal("oversized pending state accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed save modified pending record")
	}
}
