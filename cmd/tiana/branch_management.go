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

func newBranchMutationCommand(action string, input io.Reader, output, diagnostics io.Writer) *cli.Command {
	flags := []cli.Flag{}
	usage, description := "Create a SQLite branch", "Create a child of the default branch, or the exact branch name selected by --parent. Success means asynchronous acceptance; check branch list before connecting."
	if action == "create" {
		flags = append(flags, stringOption("parent", "Exact parent branch name; omitted selects the default branch", ""))
	} else {
		usage = "Delete a SQLite branch"
		description = "Delete the exact branch name, or immutable ID with --by-id. Requires terminal confirmation unless -f is set. Default and protected branches cannot be deleted. Success means asynchronous acceptance."
		flags = append(flags, &cli.BoolFlag{Name: "force", Aliases: []string{"f"}, Usage: "Skip deletion confirmation", Local: true}, boolOption("by-id", "Interpret BRANCH as an immutable branch ID"))
	}
	return &cli.Command{Name: action, Usage: usage, Description: description, ArgsUsage: "INSTANCE BRANCH", Flags: flags, Action: func(ctx context.Context, cmd *cli.Command) error {
		if cmd.NArg() != 2 || strings.TrimSpace(cmd.Args().Get(0)) == "" || strings.TrimSpace(cmd.Args().Get(1)) == "" {
			return argumentFailure(ctx, cmd, "one instance ID or name and one branch name are required")
		}
		for _, arg := range cmd.Args().Slice() {
			if positionalWasTrimmed(cmd, arg) {
				return argumentFailure(ctx, cmd, "use -- before names to preserve surrounding whitespace")
			}
		}
		if action == "create" && cmd.IsSet("parent") && strings.TrimSpace(cmd.String("parent")) == "" {
			return argumentFailure(ctx, cmd, "parent branch name cannot be empty")
		}
		if action == "delete" && cmd.Bool("by-id") && !authclient.ValidResourcePathID(cmd.Args().Get(1)) {
			return argumentFailure(ctx, cmd, "invalid branch ID")
		}
		return statusError(executeBranchMutation(ctx, action, cmd.Args().Get(0), cmd.Args().Get(1), cmd.String("parent"), cmd.Bool("by-id"), cmd.Bool("force"), input, output, diagnostics))
	}}
}

func executeBranchMutation(ctx context.Context, action, reference, name, parent string, byID, force bool, input io.Reader, output, diagnostics io.Writer) int {
	if action == "delete" && !force && !isTerminal(input) {
		fmt.Fprintln(diagnostics, "tiana: deletion requires terminal confirmation; use --force (-f) for non-interactive deletion")
		return 2
	}
	client, err := newAuthClient(ctx, diagnostics, !isTerminal(input))
	if err != nil {
		writeCommandError(diagnostics, err)
		return 1
	}
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
		var e error
		instance, e = resolveDeletionTarget(ctx, client, reference, sqliteManagementScope)
		return e
	}
	err = client.RunAuthenticated(ctx, resolve)
	if errors.Is(err, authclient.ErrAuthenticationRequired) {
		err = client.RunAuthenticated(ctx, resolve)
	}
	if err != nil {
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
		writeCommandError(diagnostics, err)
		return 1
	}
	branch := detail.Branch
	if action == "delete" {
		if branch.ID == "main" || branch.Root || branch.Protected {
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
		receipt, err = client.CreateBranch(ctx, instance.ID, branch.ID, name, key)
	} else {
		receipt, err = client.DeleteBranch(ctx, instance.ID, branch.ID, key)
	}
	if err != nil {
		writeCommandError(diagnostics, err)
		fmt.Fprintf(diagnostics, "Branch %s was not confirmed: instance=%s branch=%s. Check branch list before retrying; do not blindly repeat creation. For deletion, retry only this branch ID with --by-id.\n", action, safeDisplay(instance.ID), safeDisplay(branch.ID))
		if ctx.Err() != nil {
			return 130
		}
		return 1
	}
	if _, err := fmt.Fprintf(output, "Branch %s accepted: instance=%s operation=%s\n", action, safeDisplay(receipt.InstanceID), safeDisplay(receipt.OperationID)); err != nil {
		fmt.Fprintln(diagnostics, "tiana: operation accepted but cannot write receipt; check branch list before retrying")
		return 1
	}
	return 0
}
