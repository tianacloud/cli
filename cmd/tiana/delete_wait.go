package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/urfave/cli/v3"
)

func deleteWaitOption() cli.Flag {
	return &cli.BoolFlag{Name: "wait", Aliases: []string{"w"}, Usage: "Wait for the deletion operation to succeed; Ctrl-C stops waiting", Local: true}
}

var errOperationFailed = errors.New("operation failed; inspect the operation before retrying")

type deletionObserver interface {
	GetInstanceOperation(context.Context, string, string) (authclient.InstanceOperation, error)
}

// The receipt identifies one accepted mutation. Never infer deletion from a
// missing operation or re-resolve a name that could have been reused.
func waitForDeletion(ctx context.Context, client deletionObserver, instanceID, operationID, kind, branchID string, interval time.Duration) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		operation, err := client.GetInstanceOperation(ctx, instanceID, operationID)
		if err != nil {
			return err
		}
		if operation.InstanceID != instanceID || operation.OperationID != operationID || operation.Kind != kind || (kind == "DELETE_BRANCH" && operation.BranchID != branchID) {
			return errors.New("deletion operation does not match the accepted target")
		}
		switch operation.State {
		case "success":
			return nil
		case "failed":
			return errOperationFailed
		case "pending", "running", "retry_wait":
		default:
			return errors.New("unrecognized deletion operation state")
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

func reportDeletionWait(ctx context.Context, client deletionObserver, instanceID, operationID, kind, branchID string, output, diagnostics io.Writer) int {
	if _, err := fmt.Fprintln(diagnostics, "Waiting for deletion; Ctrl-C stops waiting without cancelling the operation."); err != nil {
		return 1
	}
	if err := waitForDeletion(ctx, client, instanceID, operationID, kind, branchID, time.Second); err != nil {
		writeCommandError(diagnostics, err)
		fmt.Fprintf(diagnostics, "Deletion observation stopped: instance=%s operation=%s", safeDisplay(instanceID), safeDisplay(operationID))
		if branchID != "" {
			fmt.Fprintf(diagnostics, " branch=%s", safeDisplay(branchID))
		}
		fmt.Fprintln(diagnostics, ". Inspect the result before retrying with these immutable IDs.")
		if ctx.Err() != nil {
			return 130
		}
		return 1
	}
	if _, err := fmt.Fprintf(output, "Deletion succeeded: instance=%s operation=%s\n", safeDisplay(instanceID), safeDisplay(operationID)); err != nil {
		return 1
	}
	return 0
}
