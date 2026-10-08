package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/tianacloud/cli/internal/authclient"
)

type showOptions struct {
	reference      string
	branch         string
	urlOnly        bool
	nonInteractive bool
}

func executeSQLiteShow(ctx context.Context, options showOptions, output, errorOutput io.Writer) int {
	client, err := newAuthClient(ctx, errorOutput, options.nonInteractive)
	if err != nil {
		fmt.Fprintln(errorOutput, "tiana:", safeDisplay(err.Error()))
		return 1
	}
	instance, err := resolveInstanceWithLogin(ctx, client, options.reference)
	if err != nil {
		reportResolveError(errorOutput, options.reference, err)
		return 1
	}
	if err := sqliteManagementScope.check(instance); err != nil {
		writeCommandError(errorOutput, err)
		return 1
	}
	state := instanceDisplayState(instance)
	if !options.urlOnly && options.branch == "" && (state == "DELETING" || state == "DELETED") {
		// The instance remains inspectable after its default branch is removed.
		if err := printInstanceDetail(output, instance); err != nil {
			writeCommandError(errorOutput, err)
			return 1
		}
		return 0
	}
	detail, err := client.ResolveBranch(ctx, instance.ID, options.branch)
	if err != nil {
		reportResolveError(errorOutput, options.reference, err)
		return 1
	}
	instance.EndpointID = detail.Branch.EndpointID
	instance.Connection = detail.Connection
	if options.urlOnly {
		url, ready := instanceConnectionURL(instance)
		if !ready {
			fmt.Fprintf(errorOutput, "tiana: instance %s has no connection URL yet\n", safeDisplay(instance.ID))
			return 1
		}
		if _, err := fmt.Fprintln(output, url); err != nil {
			writeCommandError(errorOutput, err)
			return 1
		}
		return 0
	}
	if err := printInstanceDetail(output, instance); err != nil {
		writeCommandError(errorOutput, err)
		return 1
	}
	_, err = fmt.Fprintf(output, "Branch name:    %s\nBranch ID:      %s\nDefault branch: %t\nBranch state:   %s\nBranch runtime: %s\nEndpoint:       %s\n", safeDisplay(detail.Branch.Name), safeDisplay(detail.Branch.ID), detail.Branch.Root, safeDisplay(detail.Branch.LifecycleState), safeDisplay(detail.Branch.RuntimeState), safeDisplay(detail.Branch.EndpointID))
	if err != nil {
		writeCommandError(errorOutput, err)
		return 1
	}
	return 0
}

func printInstanceDetail(output io.Writer, instance authclient.Instance) error {
	connection := connectionURLColumn(instance)
	if _, err := fmt.Fprintf(output, "Name:           %s\n", safeDisplay(instance.DisplayName)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "ID:             %s\n", safeDisplay(instance.ID)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "Engine:         %s\n", safeDisplay(instance.Engine)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "Product state:  %s\n", safeDisplay(instanceDisplayState(instance))); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "Runtime status: %s\n", safeDisplay(runtimeStateDescription(instance))); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "Connection URL: %s\n", safeDisplay(connection)); err != nil {
		return err
	}
	if instance.CreatedAt != "" {
		if _, err := fmt.Fprintf(output, "Created at:     %s\n", safeDisplay(instance.CreatedAt)); err != nil {
			return err
		}
	}
	return nil
}

// resolveInstanceWithLogin resolves a reference, completing browser
// authentication first when the session is absent or revoked.
func resolveInstanceWithLogin(ctx context.Context, client *authclient.Client, reference string) (authclient.Instance, error) {
	var instance authclient.Instance
	operation := func(operationContext context.Context, _ authclient.Credential) error {
		resolved, err := client.ResolveInstance(operationContext, reference)
		if err != nil {
			return err
		}
		instance = resolved
		return nil
	}
	err := client.RunAuthenticated(ctx, operation)
	if errors.Is(err, authclient.ErrAuthenticationRequired) {
		err = client.RunAuthenticated(ctx, operation)
	}
	if err != nil {
		return authclient.Instance{}, err
	}
	return instance, nil
}

func reportResolveError(errorOutput io.Writer, reference string, err error) {
	var duplicate *authclient.DuplicateInstanceNameError
	if errors.As(err, &duplicate) {
		fmt.Fprintf(errorOutput, "tiana: instance name %q matches %d instances; specify an instance ID\n", duplicate.Name, duplicate.Count)
		for _, id := range duplicate.CandidateIDs {
			fmt.Fprintf(errorOutput, "  %s\n", safeDisplay(id))
		}
		return
	}
	if errors.Is(err, authclient.ErrInstanceNotFound) {
		fmt.Fprintf(errorOutput, "tiana: instance not found: %s\n", safeDisplay(reference))
		return
	}
	writeCommandError(errorOutput, err)
}
