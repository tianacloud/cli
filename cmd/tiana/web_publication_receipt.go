package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
	"path/filepath"
)

// One latest receipt per account/Web keeps interrupted publish queryable. A new
// explicit publish replaces it; no command resumes or replays the archive PUT.
func persistWebPublication(ctx context.Context, receipt apppublish.PublicationReceipt) error {
	base, err := newPendingStore()
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(receipt.Origin + "\x00" + receipt.TenantID + "\x00" + receipt.UserID + "\x00" + receipt.WebID))
	store := authclient.NewFilePendingCommandStore(filepath.Join(filepath.Dir(base.Path), "web-publish-receipts", hex.EncodeToString(sum[:])+".json"))
	unlock, err := store.Acquire(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return store.Save(authclient.PendingCommand{Command: "web.publish.receipt", Args: []string{receipt.WebID, receipt.SHA256, receipt.ArchivePath}, IdempotencyKey: receipt.PublishID, InstanceID: receipt.InstanceID, Origin: receipt.Origin, TenantID: receipt.TenantID, UserID: receipt.UserID, CreatedAt: receipt.CreatedAt})
}
