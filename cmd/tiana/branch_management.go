package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
	"github.com/urfave/cli/v3"
)

func newBranchMutationCommand(action string, input io.Reader, output, diagnostics io.Writer) *cli.Command {
	flags := []cli.Flag{boolOption("json", "Write a structured JSON result")}
	usage, description := "Create a SQLite branch", "Create a child of the default branch, or the exact branch name selected by --parent. By default success means asynchronous acceptance; use -w/--wait to wait for creation to succeed."
	if action == "create" {
		flags = append(flags, branchCreateOptions()...)
		flags = append(flags, createWaitOption(), stringOption("parent", "Exact parent branch name; omitted selects the default branch", ""))
	} else {
		usage = "Delete a SQLite branch"
		description = "Delete the exact branch name, or immutable ID with --by-id. Requires terminal confirmation unless -f is set. Default and protected branches cannot be deleted. By default success means asynchronous acceptance; -w/--wait waits for the deletion operation to succeed."
		flags = append(flags, deleteWaitOption(), &cli.BoolFlag{Name: "force", Aliases: []string{"f"}, Usage: "Skip deletion confirmation", Local: true}, boolOption("by-id", "Interpret BRANCH as an immutable branch ID"))
	}
	return &cli.Command{Name: action, Usage: usage, Description: description, ArgsUsage: "INSTANCE BRANCH", Flags: flags, Action: func(ctx context.Context, cmd *cli.Command) error {
		if cmd.NArg() != 2 || strings.TrimSpace(cmd.Args().Get(0)) == "" || strings.TrimSpace(cmd.Args().Get(1)) == "" {
			return argumentFailure(ctx, cmd, "one instance ID or name and one branch name are required")
		}
		for _, arg := range cmd.Args().Slice() {
			if branchNameWasTrimmed(cmd, arg) {
				return argumentFailure(ctx, cmd, "use -- before names to preserve surrounding whitespace")
			}
		}
		if action == "create" && cmd.IsSet("parent") && strings.TrimSpace(cmd.String("parent")) == "" {
			return argumentFailure(ctx, cmd, "parent branch name cannot be empty")
		}
		if action == "delete" && cmd.Bool("by-id") && !authclient.ValidResourcePathID(cmd.Args().Get(1)) {
			return argumentFailure(ctx, cmd, "invalid branch ID")
		}
		return statusError(executeBranchMutation(ctx, action, cmd.Args().Get(0), cmd.Args().Get(1), cmd.String("parent"), cmd.Bool("by-id"), cmd.Bool("force"), cmd.Bool("wait"), branchCreateRequest(cmd, cmd.Args().Get(1)), input, output, diagnostics))
	}}
}

func executeBranchMutation(ctx context.Context, action, reference, name, parent string, byID, force, wait bool, create authclient.CreateBranchRequest, input io.Reader, output, diagnostics io.Writer) int {
	if managementJSON(ctx) && action == "delete" && !force {
		return writeAppResult(apppublish.Failure(&apppublish.Error{Code: "CONFIRMATION_REQUIRED", Message: "JSON deletion requires --force", NextAction: "Specify --force only after confirming deletion of this branch", ExitCode: 2}), true, output, diagnostics)
	}
	if action == "delete" && !force && !isTerminal(input) {
		fmt.Fprintln(diagnostics, "tiana: deletion requires terminal confirmation; use --force (-f) for non-interactive deletion")
		return 2
	}
	client, err := newAuthClient(ctx, diagnostics, managementJSON(ctx) || !isTerminal(input))
	if err != nil {
		if managementJSON(ctx) {
			return managementFailure(err, output, diagnostics, nil)
		}
		writeCommandError(diagnostics, err)
		return 1
	}
	store, err := newPendingStore()
	if err != nil {
		if managementJSON(ctx) {
			return managementFailure(err, output, diagnostics, nil)
		}
		writeCommandError(diagnostics, err)
		return 1
	}
	release, err := store.Acquire(ctx)
	if err != nil {
		if managementJSON(ctx) {
			return managementFailure(err, output, diagnostics, nil)
		}
		writeCommandError(diagnostics, err)
		return 1
	}
	defer release()
	pending, err := store.Load()
	if err == nil {
		if managementJSON(ctx) {
			return writeAppResult(apppublish.Failure(&apppublish.Error{Code: "PENDING_COMMAND", Message: "An unresolved command blocks this mutation", NextAction: "Recover the original pending command before another write", ExitCode: 1}), true, output, diagnostics)
		}
		reportUnfinishedOperation(diagnostics, store.Path, pending)
		return 1
	}
	if !errors.Is(err, authclient.ErrPendingNotFound) {
		if managementJSON(ctx) {
			return managementFailure(err, output, diagnostics, nil)
		}
		writeCommandError(diagnostics, err)
		return 1
	}
	var instance authclient.Instance
	resolve := func(ctx context.Context, _ authclient.Credential) error {
		var e error
		instance, e = resolveDeletionTarget(ctx, client, reference, sqliteManagementScope)
		return e
	}
	err = client.RunAuthenticated(ctx, resolve)
	if errors.Is(err, authclient.ErrAuthenticationRequired) {
		err = client.RunAuthenticated(ctx, resolve)
	}
	if err != nil {
		if managementJSON(ctx) {
			return managementFailure(err, output, diagnostics, nil)
		}
		reportResolveError(diagnostics, reference, err)
		return 1
	}
	target, targetByID := name, byID
	if action == "create" {
		target = parent
		targetByID = false
		if parent == "" {
			target = "main"
			targetByID = true
		}
	}
	detail, err := client.ResolveMutationBranch(ctx, instance.ID, target, targetByID)
	if err != nil {
		if managementJSON(ctx) {
			return managementFailure(err, output, diagnostics, nil)
		}
		writeCommandError(diagnostics, err)
		return 1
	}
	branch := detail.Branch
	if action == "delete" {
		if branch.ID == "main" || branch.Root || branch.Protected {
			if managementJSON(ctx) {
				return writeAppResult(apppublish.Failure(&apppublish.Error{Code: "PROTECTED_BRANCH", Message: "Cannot delete a default or protected branch", ExitCode: 1}), true, output, diagnostics)
			}
			fmt.Fprintln(diagnostics, "tiana: cannot delete a default or protected branch")
			return 1
		}
		if !force {
			if _, err := fmt.Fprintf(diagnostics, "Delete SQLite branch %q (%s) in instance %s and its data? [y/N] ", safeDisplay(branch.Name), safeDisplay(branch.ID), safeDisplay(instance.ID)); err != nil {
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
	}
	if ctx.Err() != nil {
		fmt.Fprintln(diagnostics, "Branch operation cancelled.")
		return 130
	}
	key, err := authclient.NewIdempotencyKey()
	if err != nil {
		writeCommandError(diagnostics, err)
		return 1
	}
	var receipt authclient.BranchOperationReceipt
	if action == "create" {
		receipt, err = client.CreateBranch(ctx, instance.ID, branch.ID, create, key)
	} else {
		receipt, err = client.DeleteBranch(ctx, instance.ID, branch.ID, key)
	}
	if err != nil {
		if managementJSON(ctx) {
			return mutationJSONFailure(ctx, err, map[string]string{"instance_id": instance.ID, "branch_id": branch.ID, "request_id": key}, output, diagnostics)
		}
		writeCommandError(diagnostics, err)
		fmt.Fprintf(diagnostics, "Branch %s was not confirmed: instance=%s branch=%s. Check branch list before retrying; do not blindly repeat creation. For deletion, retry only this branch ID with --by-id.\n", action, safeDisplay(instance.ID), safeDisplay(branch.ID))
		if ctx.Err() != nil {
			return 130
		}
		return 1
	}
	if managementJSON(ctx) {
		data := map[string]string{"instance_id": receipt.InstanceID, "operation_id": receipt.OperationID}
		if action == "delete" {
			data["branch_id"] = branch.ID
		} else {
			data["parent_branch_id"] = branch.ID
		}
		if !wait {
			return writeAppResult(apppublish.Result{Status: "accepted", Data: data}, true, output, diagnostics)
		}
		fmt.Fprintf(diagnostics, "Branch %s accepted: instance=%s operation=%s\n", action, safeDisplay(receipt.InstanceID), safeDisplay(receipt.OperationID))
		var observed error
		if action == "delete" {
			observed = waitForDeletion(ctx, client, receipt.InstanceID, receipt.OperationID, "DELETE_BRANCH", branch.ID, time.Second)
		} else {
			observed = waitForBranchCreation(ctx, client, receipt, branch.ID, time.Second)
		}
		return observedMutationJSON(ctx, observed, data, output, diagnostics)
	}
	if _, err := fmt.Fprintf(output, "Branch %s accepted: instance=%s operation=%s\n", action, safeDisplay(receipt.InstanceID), safeDisplay(receipt.OperationID)); err != nil {
		fmt.Fprintln(diagnostics, "tiana: operation accepted but cannot write receipt; check branch list before retrying")
		return 1
	}
	if action == "delete" && wait {
		return reportDeletionWait(ctx, client, receipt.InstanceID, receipt.OperationID, "DELETE_BRANCH", branch.ID, output, diagnostics)
	}
	if action == "create" && wait {
		if _, err := fmt.Fprintln(diagnostics, "Waiting for branch creation; Ctrl-C stops waiting without cancelling the operation."); err != nil {
			return 1
		}
		if err := waitForBranchCreation(ctx, client, receipt, branch.ID, time.Second); err != nil {
			writeCommandError(diagnostics, err)
			fmt.Fprintf(diagnostics, "Creation observation stopped: instance=%s operation=%s. Check branch list and the operation before retrying; do not blindly repeat creation.\n", safeDisplay(receipt.InstanceID), safeDisplay(receipt.OperationID))
			if ctx.Err() != nil {
				return 130
			}
			return 1
		}
		if _, err := fmt.Fprintf(output, "Branch creation succeeded: instance=%s operation=%s\n", safeDisplay(receipt.InstanceID), safeDisplay(receipt.OperationID)); err != nil {
			return 1
		}
	}

	return 0
}
