package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tianacloud/cli/internal/authclient"
)

func TestPendingCommandsAndLocksAreIndependentByOrigin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("TIANA_PENDING_COMMAND_FILE", "")
	firstOrigin := "https://first.example.test"
	t.Setenv("TIANA_API_ORIGIN", firstOrigin)
	first, err := newPendingStore()
	if err != nil {
		t.Fatal(err)
	}
	releaseFirst, err := first.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst()
	if err := first.Save(authclient.PendingCommand{Command: "sqlite.create", Origin: firstOrigin, IdempotencyKey: "first-request"}); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TIANA_API_ORIGIN", "https://second.example.test")
	second, err := newPendingStore()
	if err != nil {
		t.Fatal(err)
	}
	releaseSecond, err := second.Acquire(context.Background())
	if err != nil {
		t.Fatalf("other origin was blocked: %v", err)
	}
	defer releaseSecond()
	if err := second.Save(authclient.PendingCommand{Command: "git.create", Origin: "https://second.example.test", IdempotencyKey: "second-request"}); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TIANA_API_ORIGIN", firstOrigin+"/")
	again, err := newPendingStore()
	if err != nil {
		t.Fatal(err)
	}
	if again.Path != first.Path {
		t.Fatal("same origin changed pending location")
	}
	if release, err := again.Acquire(context.Background()); err == nil {
		release()
		t.Fatal("same origin acquired an occupied lock")
	}
	for _, tc := range []struct {
		store *authclient.FilePendingCommandStore
		key   string
	}{{first, "first-request"}, {second, "second-request"}} {
		got, err := tc.store.Load()
		if err != nil || got.IdempotencyKey != tc.key {
			t.Fatalf("pending overwritten: %v", err)
		}
	}
}

func TestExplicitPendingFileIsUsed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	t.Setenv("TIANA_PENDING_COMMAND_FILE", path)
	for _, origin := range []string{"https://first.example.test", "https://second.example.test"} {
		t.Setenv("TIANA_API_ORIGIN", origin)
		store, err := newPendingStore()
		if err != nil || store.Path != path {
			t.Fatalf("explicit pending file changed: %v", err)
		}
	}
}
