package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/urfave/cli/v3"
)

func newInstanceDeleteCommand(input io.Reader, output, diagnostics io.Writer, scope databaseScope) *cli.Command {
	return &cli.Command{Name: "delete", Usage: "Delete an instance", ArgsUsage: "INSTANCE",
		Description: "Delete the entire instance, including its branches and data. Accepts an ID or exact name. Requires terminal confirmation unless --force is set. By default success means asynchronous acceptance; -w/--wait waits for the deletion operation to succeed.",
		Flags:       []cli.Flag{deleteWaitOption(), &cli.BoolFlag{Name: "force", Aliases: []string{"f"}, Usage: "Skip deletion confirmation", Local: true}},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 || strings.TrimSpace(cmd.Args().First()) == "" {
				return argumentFailure(ctx, cmd, "one instance ID or name is required")
			}
			if positionalWasTrimmed(cmd, cmd.Args().First()) {
				return argumentFailure(ctx, cmd, "use -- before the instance name to preserve surrounding whitespace")
			}
			return statusError(executeInstanceDelete(ctx, cmd.Args().First(), cmd.Bool("force"), cmd.Bool("wait"), input, output, diagnostics, scope))
		},
	}
}

func executeInstanceDelete(ctx context.Context, reference string, force, wait bool, input io.Reader, output, diagnostics io.Writer, scope databaseScope) int {
	if !force && !isTerminal(input) {
		fmt.Fprintln(diagnostics, "tiana: deletion requires terminal confirmation; use --force (-f) for non-interactive deletion")
		return 2
	}
	client, err := newAuthClient(ctx, diagnostics, !isTerminal(input))
	if err != nil {
		writeCommandError(diagnostics, err)
		return 1
	}
	// Serialize with other mutation commands and preserve unresolved intents. MGR
	// durably deduplicates deletion by immutable instance ID; no new local schema.
	store, err := newPendingStore()
	if err != nil {
		writeCommandError(diagnostics, err)
		return 1
	}
	release, err := store.Acquire(ctx)
	if err != nil {
		writeCommandError(diagnostics, err)
		return 1
	}
	defer release()
	pending, err := store.Load()
	if err == nil {
		reportUnfinishedOperation(diagnostics, store.Path, pending)
		return 1
	}
	if !errors.Is(err, authclient.ErrPendingNotFound) {
		writeCommandError(diagnostics, err)
		return 1
	}
	var instance authclient.Instance
	resolve := func(ctx context.Context, _ authclient.Credential) error {
		var err error
		instance, err = resolveDeletionTarget(ctx, client, reference, scope)
		return err
	}
	err = client.RunAuthenticated(ctx, resolve)
	if errors.Is(err, authclient.ErrAuthenticationRequired) {
		err = client.RunAuthenticated(ctx, resolve)
	}
	if err != nil {
		reportResolveError(diagnostics, reference, err)
		return 1
	}
	if !force {
		if _, err := fmt.Fprintf(diagnostics, "Delete %s instance %q (%s), including all branches and data? [y/N] ", scope.engine, safeDisplay(instance.DisplayName), safeDisplay(instance.ID)); err != nil {
			return 1
		}
		accepted, err := readDeleteConfirmation(ctx, input.(*os.File))
		if err != nil {
			if ctx.Err() != nil {
				fmt.Fprintln(diagnostics, "\nDeletion cancelled.")
				return 130
			}
			fmt.Fprintln(diagnostics, "\ntiana: cannot read deletion confirmation")
			return 1
		}
		if !accepted {
			fmt.Fprintln(diagnostics, "Deletion cancelled.")
			return 0
		}
	}
	if ctx.Err() != nil {
		fmt.Fprintln(diagnostics, "Deletion cancelled.")
		return 130
	}
	key, err := authclient.NewIdempotencyKey()
	if err != nil {
		writeCommandError(diagnostics, err)
		return 1
	}
	// Pin the resolved ID across confirmation and the SDK's 401 refresh retry.
	// No name re-resolution or command-level retry on transport/server failure.
	receipt, err := client.DeleteInstance(ctx, instance.ID, key)
	if err != nil {
		writeCommandError(diagnostics, err)
		fmt.Fprintf(diagnostics, "Deletion was not confirmed for instance %s; check its status before retrying with this ID.\n", safeDisplay(instance.ID))
		if ctx.Err() != nil {
			return 130
		}
		return 1
	}
	if _, err := fmt.Fprintf(output, "Deletion accepted: instance=%s operation=%s\n", safeDisplay(receipt.InstanceID), safeDisplay(receipt.OperationID)); err != nil {
		fmt.Fprintln(diagnostics, "tiana: deletion accepted but cannot write receipt; inspect the result before retrying")
		return 1
	}
	if wait {
		return reportDeletionWait(ctx, client, receipt.InstanceID, receipt.OperationID, "DELETE_INSTANCE", "", output, diagnostics)
	}
	return 0
}

// Resolve without branch lookup: deletion targets the whole instance. Name
// matching is exact and product scoped, and ambiguities never choose a target.
func resolveDeletionTarget(ctx context.Context, client *authclient.Client, reference string, scope databaseScope) (authclient.Instance, error) {
	reference = strings.TrimSpace(reference)
	instance, err := client.GetInstance(ctx, reference)
	if err == nil {
		if instance.ID != reference {
			return authclient.Instance{}, errors.New("instance response does not match the requested ID")
		}
		return instance, scope.check(instance)
	}
	if !errors.Is(err, authclient.ErrInstanceNotFound) && !errors.Is(err, authclient.ErrInstanceInvalidID) {
		return authclient.Instance{}, err
	}
	var match *authclient.Instance
	for page := 1; ; page++ {
		result, err := client.ListInstances(ctx, reference, page, nonInteractivePageSize)
		if err != nil {
			return authclient.Instance{}, err
		}
		for _, item := range result.Items {
			if item.Engine != scope.engine || item.DisplayName != reference {
				continue
			}
			if item.ID == "" {
				return authclient.Instance{}, errors.New("instance response has no ID")
			}
			if match != nil {
				return authclient.Instance{}, &authclient.DuplicateInstanceNameError{Name: reference, Count: 2, CandidateIDs: []string{match.ID, item.ID}}
			}
			value := item
			match = &value
		}
		if len(result.Items) == 0 || result.TotalPages == 0 || page >= result.TotalPages {
			break
		}
	}
	if match == nil {
		return authclient.Instance{}, authclient.ErrInstanceNotFound
	}
	return *match, nil
}
