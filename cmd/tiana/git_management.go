package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/gitremote"
	"github.com/urfave/cli/v3"
)

var gitManagementScope = databaseScope{engine: "git"}

func newGitCommand(input io.Reader, output, diagnostics io.Writer) *cli.Command {
	return &cli.Command{Name: "git", Usage: "Git service commands", Action: groupAction, Commands: []*cli.Command{
		newGitCreateCommand(output, diagnostics),
		newGitListCommand(input, output, diagnostics),
		newGitShowCommand(output, diagnostics),
		newInstanceDeleteCommand(input, output, diagnostics, gitManagementScope),
		{Name: "remote-helper", Usage: "Run the Git remote helper", ArgsUsage: "<remote> [url]", SkipFlagParsing: true,
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if cmd.NArg() == 1 && isHelp(cmd.Args().Slice()) {
					return showHelp(ctx, cmd)
				}
				reader, ok := input.(io.ReadCloser)
				if !ok {
					reader = io.NopCloser(input)
				}
				if err := gitremote.Run(ctx, cmd.Args().Slice(), reader, output); err != nil {
					fmt.Fprintln(diagnostics, "tiana git remote-helper:", safeDisplay(err.Error()))
					if ctx.Err() != nil {
						return statusError(130)
					}
					return statusError(1)
				}
				return nil
			}},
	}}
}

func newGitCreateCommand(output, diagnostics io.Writer) *cli.Command {
	return &cli.Command{Name: "create", Usage: "Submit Git instance creation", ArgsUsage: "NAME",
		Flags:       []cli.Flag{createWaitOption(), createMessageOption()},
		Description: "NAME is required as a positional argument. The git engine is fixed. Returns after acceptance by default; -w/--wait waits for creation success. Never creates a Token.",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() > 1 {
				return argumentFailure(ctx, cmd, "the repository name was provided more than once")
			}
			name := cmd.Args().First()
			if instanceCreateNameWasTrimmed(cmd, name) {
				return argumentFailure(ctx, cmd, "use -- to preserve surrounding whitespace")
			}
			if strings.TrimSpace(name) == "" {
				return argumentFailure(ctx, cmd, "a repository name is required")
			}
			options := createOptions{wait: cmd.Bool("wait"), input: authclient.CreateInstanceRequest{DisplayName: name, Notes: cmd.String("message"), Engine: "git", Config: map[string]interface{}{}}}
			return statusError(executeInstanceCreate(ctx, options, leafArguments(cmd), output, diagnostics, gitManagementScope))
		},
	}
}

func newGitListCommand(input io.Reader, output, diagnostics io.Writer) *cli.Command {
	return &cli.Command{Name: "list", Usage: "List Git instances only", Description: "An interactive terminal pages the results; otherwise all pages are printed once.",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 0 {
				return argumentFailure(ctx, cmd, "list does not accept arguments")
			}
			return statusError(executeInstanceList(ctx, listOptions{}, input, output, diagnostics, gitManagementScope))
		},
	}
}

func newGitShowCommand(output, diagnostics io.Writer) *cli.Command {
	return &cli.Command{Name: "show", Usage: "Show a Git instance", ArgsUsage: "INSTANCE", Description: "Use an instance ID or exact name. With --url, stdout contains only the Git connection URL.", Flags: []cli.Flag{boolOption("url", "Print only the Git connection URL")},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 || strings.TrimSpace(cmd.Args().First()) == "" {
				return argumentFailure(ctx, cmd, "one instance ID or name is required")
			}
			if positionalWasTrimmed(cmd, cmd.Args().First()) {
				return argumentFailure(ctx, cmd, "use -- before the instance name to preserve surrounding whitespace")
			}
			return statusError(executeGitShow(ctx, cmd.Args().First(), cmd.Bool("url"), output, diagnostics))
		},
	}
}

func executeGitShow(ctx context.Context, reference string, urlOnly bool, output, diagnostics io.Writer) int {
	client, err := newAuthClient(ctx, diagnostics, false)
	if err != nil {
		writeCommandError(diagnostics, err)
		return 1
	}
	instance, err := resolveGitInstanceWithLogin(ctx, client, reference)
	if err != nil {
		reportResolveError(diagnostics, reference, err)
		return 1
	}
	if err := gitManagementScope.check(instance); err != nil {
		writeCommandError(diagnostics, err)
		return 1
	}
	if urlOnly {
		locator, ready := instanceConnectionURL(instance)
		if !ready {
			fmt.Fprintln(diagnostics, "tiana: Git instance has no valid connection URL yet")
			return 1
		}
		if _, err := fmt.Fprintln(output, locator); err != nil {
			writeCommandError(diagnostics, err)
			return 1
		}
	} else {
		if err := printInstanceDetail(output, instance); err != nil {
			writeCommandError(diagnostics, err)
			return 1
		}
	}
	return 0
}

// Git name lookup requires MGR display_name filtering before pagination.
// Keep at most two matches: enough to reject ambiguity without an unbounded list.
func resolveGitInstanceWithLogin(ctx context.Context, client *authclient.Client, reference string) (authclient.Instance, error) {
	reference = strings.TrimSpace(reference)
	var instance authclient.Instance
	operation := func(ctx context.Context, _ authclient.Credential) error {
		found, err := client.GetInstance(ctx, reference)
		if err == nil {
			instance = found
			return nil
		}
		if !errors.Is(err, authclient.ErrInstanceNotFound) && !errors.Is(err, authclient.ErrInstanceInvalidID) {
			return err
		}
		var matches []authclient.Instance
		for page := 1; ; page++ {
			result, err := client.ListInstances(ctx, reference, page, nonInteractivePageSize)
			if err != nil {
				return err
			}
			for _, candidate := range result.Items {
				if candidate.Engine == "git" && candidate.DisplayName == reference {
					matches = append(matches, candidate)
					if len(matches) == 2 {
						return &authclient.DuplicateInstanceNameError{Name: reference, Count: 2, CandidateIDs: []string{matches[0].ID, matches[1].ID}}
					}
				}
			}
			if len(result.Items) == 0 || result.TotalPages == 0 || page >= result.TotalPages {
				break
			}
		}
		if len(matches) == 0 {
			return authclient.ErrInstanceNotFound
		}
		instance = matches[0]
		return nil
	}
	err := client.RunAuthenticated(ctx, operation)
	if errors.Is(err, authclient.ErrAuthenticationRequired) {
		err = client.RunAuthenticated(ctx, operation)
	}
	return instance, err
}
