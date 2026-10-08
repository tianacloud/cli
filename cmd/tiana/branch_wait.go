package main

import (
	"context"
	"errors"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

type branchCreationObserver interface {
	GetInstanceOperation(context.Context, string, string) (authclient.InstanceOperation, error)
}

func waitForBranchCreation(ctx context.Context, client branchCreationObserver, receipt authclient.BranchOperationReceipt, parentID string, interval time.Duration) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		operation, err := client.GetInstanceOperation(ctx, receipt.InstanceID, receipt.OperationID)
		if err != nil {
			return err
		}
		if operation.InstanceID != receipt.InstanceID || operation.OperationID != receipt.OperationID || operation.Kind != "CREATE_BRANCH" || operation.ParentBranchID != parentID {
			return errors.New("branch creation operation does not match the accepted request")
		}
		switch operation.State {
		case "success":
			if !authclient.ValidResourcePathID(operation.BranchID) || operation.BranchID == parentID {
				return errors.New("branch creation operation has no valid child identity")
			}
			return nil
		case "failed":
			return errors.New("branch creation failed; inspect the operation before retrying")
		case "pending", "running", "retry_wait":
		default:
			return errors.New("unrecognized branch creation operation state")
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
