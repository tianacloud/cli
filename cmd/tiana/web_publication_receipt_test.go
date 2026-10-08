package main

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPublicationReceiptIsPrivateAndIsolatedFromOtherAccounts(t *testing.T) {
	env := newTestEnv(t, "https://mgr.example.test")
	r := apppublish.PublicationReceipt{Origin: "https://mgr.example.test", TenantID: "tenant", UserID: "user", WebID: "web", InstanceID: "web-instance", PublishID: strings.Repeat("a", 32), SHA256: strings.Repeat("b", 64), ArchivePath: filepath.Join(filepath.Dir(env.pendingPath), "candidate.tweb"), CreatedAt: time.Now()}
	receiptPath := func(r apppublish.PublicationReceipt) string {
		hash := sha256.Sum256([]byte(r.Origin + "\x00" + r.TenantID + "\x00" + r.UserID + "\x00" + r.WebID))
		return filepath.Join(filepath.Dir(env.pendingPath), "web-publish-receipts", hex.EncodeToString(hash[:])+".json")
	}
	if err := persistWebPublication(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	p := receiptPath(r)
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("public receipt permissions: %v", info.Mode())
	}
	saved, err := authclient.NewFilePendingCommandStore(p).Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.IdempotencyKey != r.PublishID || saved.InstanceID != r.InstanceID || saved.TenantID != r.TenantID || len(saved.Args) != 3 || saved.Args[2] != r.ArchivePath {
		t.Fatalf("unrecoverable receipt: %+v", saved)
	}
	for _, change := range []func(*apppublish.PublicationReceipt){func(r *apppublish.PublicationReceipt) { r.UserID = "another" }, func(r *apppublish.PublicationReceipt) { r.TenantID = "another" }, func(r *apppublish.PublicationReceipt) { r.Origin = "https://another.example.test" }} {
		other := r
		change(&other)
		other.PublishID = strings.Repeat("c", 32)
		if err := persistWebPublication(t.Context(), other); err != nil {
			t.Fatal(err)
		}
		if receiptPath(other) == p {
			t.Fatal("receipt crossed account boundary")
		}
	}
	after, err := authclient.NewFilePendingCommandStore(p).Load()
	if err != nil || after.IdempotencyKey != r.PublishID {
		t.Fatalf("another account replaced unresolved receipt: %v", err)
	}
}
