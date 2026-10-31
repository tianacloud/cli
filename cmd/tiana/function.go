package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/functions"
	"github.com/urfave/cli/v3"
)

var functionScope = databaseScope{engine: "function"}

func functionJSONFlag() cli.Flag { return boolOption("json", "Write a structured JSON result") }

type functionAction func(context.Context, *cli.Command, *functions.Client, authclient.Instance) (apppublish.Result, error)

func functionCommand(name, usage string, flags []cli.Flag, output, diagnostics io.Writer, action functionAction) *cli.Command {
	return &cli.Command{Name: name, Usage: usage, ArgsUsage: "INSTANCE", Flags: append(flags, functionJSONFlag()), Action: func(ctx context.Context, cmd *cli.Command) error {
		if cmd.NArg() != 1 || strings.TrimSpace(cmd.Args().First()) == "" {
			return argumentFailure(ctx, cmd, "one function ID or exact name is required")
		}
		return runFunctionCommand(ctx, cmd, output, diagnostics, func(c *functions.Client) (apppublish.Result, error) {
			instance, err := c.Resolve(ctx, cmd.Args().First())
			if err != nil {
				return apppublish.Result{}, err
			}
			return action(ctx, cmd, c, instance)
		})
	}}
}

func runFunctionCommand(ctx context.Context, cmd *cli.Command, output, diagnostics io.Writer, action func(*functions.Client) (apppublish.Result, error)) error {
	var err error
	switch cmd.Name {
	case "set", "save", "publish", "delete", "enable", "disable", "update":
		var release func()
		release, err = lockFunctionMutation(ctx, diagnostics)
		if err == nil {
			defer release()
		}
	}
	var account *authclient.Client
	if err == nil {
		account, err = newAuthClient(ctx, diagnostics, cmd.Bool("json"))
	}
	var result apppublish.Result
	if err == nil {
		var c *functions.Client
		c, err = functions.Bind(ctx, account)
		if err == nil {
			result, err = action(c)
		}
	}
	if err != nil {
		if result.Status == "unknown" || result.Status == "accepted" {
			writeCommandError(diagnostics, err)
			exit := 1
			if ctx.Err() != nil {
				exit = 130
			}
			if result.Status == "accepted" {
				result.Status = "pending"
			}
			result.Error = &apppublish.Error{Code: "FUNCTION_OUTCOME_PENDING", Message: "Operation outcome is not yet confirmed", NextAction: "Inspect the function or build; recover using the original instance, request ID and source commit", ExitCode: exit}
			return statusError(writeAppResult(result, cmd.Bool("json"), output, diagnostics))
		}
		if cmd.Bool("json") {
			return statusError(managementFailure(err, output, diagnostics, result.Data))
		}
		writeCommandError(diagnostics, err)
		if ctx.Err() != nil {
			return statusError(130)
		}
		return statusError(1)
	}
	if !cmd.Bool("json") {
		if page, ok := result.Data.(authclient.InstancePage); ok {
			if err := writeInstanceTable(output, page.Items); err != nil {
				return err
			}
			_, err := fmt.Fprintf(output, "Page %d/%d · %d total\n", page.Page, page.TotalPages, page.Total)
			return err
		}
	}
	return statusError(writeAppResult(result, cmd.Bool("json"), output, diagnostics))
}

func lockFunctionMutation(ctx context.Context, diagnostics io.Writer) (func(), error) {
	store, err := newPendingStore()
	if err != nil {
		return nil, err
	}
	release, err := store.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	pending, err := store.Load()
	if errors.Is(err, authclient.ErrPendingNotFound) {
		return release, nil
	}
	release()
	if err == nil {
		reportUnfinishedOperation(diagnostics, store.Path, pending)
		err = errors.New("recover the pending operation before another mutation")
	}
	return nil, err
}

func newFunctionCommand(input io.Reader, output, diagnostics io.Writer) *cli.Command {
	return &cli.Command{Name: "function", Usage: "Manage functions, source code and releases", Action: groupAction, Commands: []*cli.Command{
		newFunctionCreateCommand(output, diagnostics),
		{Name: "list", Usage: "List one page of functions", Flags: []cli.Flag{&cli.IntFlag{Name: "page", Value: 1, Usage: "Page number"}, &cli.IntFlag{Name: "page-size", Value: 20, Usage: "Results per page (1–100)"}, functionJSONFlag()}, Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 0 || cmd.Int("page") < 1 || cmd.Int("page-size") < 1 || cmd.Int("page-size") > 100 {
				return argumentFailure(ctx, cmd, "list requires a positive page and page-size from 1 to 100")
			}
			return runFunctionCommand(ctx, cmd, output, diagnostics, func(c *functions.Client) (apppublish.Result, error) {
				page, err := c.List(ctx, "", cmd.Int("page"), cmd.Int("page-size"))
				return apppublish.Success(page), err
			})
		}},
		functionCommand("show", "Show function metadata, source binding and release", nil, output, diagnostics, func(ctx context.Context, cmd *cli.Command, c *functions.Client, i authclient.Instance) (apppublish.Result, error) {
			details, err := c.Details(ctx, i.ID)
			var release any
			if err == nil {
				err = c.Request(ctx, "GET", functions.Path(i.ID)+"/release", nil, &release)
			}
			return apppublish.Success(map[string]any{"instance": i, "function": details, "release": release}), err
		}),
		newFunctionSourceCommand(output, diagnostics),
		newFunctionPublishCommand(output, diagnostics),
		functionCommand("builds", "List build history (20 records per page)", []cli.Flag{&cli.StringFlag{Name: "before", Usage: "Return builds before this sequence"}}, output, diagnostics, func(ctx context.Context, cmd *cli.Command, c *functions.Client, i authclient.Instance) (apppublish.Result, error) {
			page, err := c.Builds(ctx, i.ID, cmd.String("before"))
			return apppublish.Success(page), err
		}),
		functionCommand("logs", "Read a build's logs", []cli.Flag{&cli.StringFlag{Name: "build", Required: true, Usage: "Submission ID from publish or builds"}}, output, diagnostics, func(ctx context.Context, cmd *cli.Command, c *functions.Client, i authclient.Instance) (apppublish.Result, error) {
			result, err := c.Account.FunctionLogs(ctx, i.ID, cmd.String("build"), c.Credential)
			return apppublish.Success(result), err
		}),
		newFunctionConfigCommand("parameters", output, diagnostics),
		newFunctionConfigCommand("cors", output, diagnostics),
		newFunctionEnabledCommand(true, output, diagnostics),
		newFunctionEnabledCommand(false, output, diagnostics),
		newFunctionDeleteCommand(input, output, diagnostics),
	}}
}

func newFunctionCreateCommand(output, diagnostics io.Writer) *cli.Command {
	return &cli.Command{Name: "create", Usage: "Create a Go function", ArgsUsage: "NAME", Flags: []cli.Flag{createWaitOption(), createMessageOption(), functionJSONFlag(), &cli.StringFlag{Name: "git-instance-id", Usage: "Existing Git instance ID; omit to create a source repository"}, &cli.StringFlag{Name: "source-dir", Usage: "Source directory in Git; defaults to /functions/<name>/; use / for repository root"}}, Action: func(ctx context.Context, cmd *cli.Command) error {
		if cmd.NArg() != 1 || strings.TrimSpace(cmd.Args().First()) == "" {
			return argumentFailure(ctx, cmd, "one function name is required")
		}
		options := createOptions{json: cmd.Bool("json"), wait: cmd.Bool("wait"), input: authclient.CreateInstanceRequest{DisplayName: cmd.Args().First(), Notes: cmd.String("message"), Engine: "function", GitInstanceID: cmd.String("git-instance-id"), SourceDir: cmd.String("source-dir"), Config: map[string]interface{}{}}}
		return statusError(executeInstanceCreate(ctx, options, leafArguments(cmd), output, diagnostics, functionScope))
	}}
}

func newFunctionConfigCommand(kind string, output, diagnostics io.Writer) *cli.Command {
	return &cli.Command{Name: kind, Usage: "Read or replace function " + kind, Action: groupAction, Commands: []*cli.Command{
		functionCommand("get", "Read "+kind, nil, output, diagnostics, func(ctx context.Context, _ *cli.Command, c *functions.Client, i authclient.Instance) (apppublish.Result, error) {
			var result any
			err := c.Request(ctx, "GET", functions.Path(i.ID)+"/"+kind, nil, &result)
			return apppublish.Success(result), err
		}),
		functionCommand("set", "Replace "+kind+" from a JSON file", []cli.Flag{&cli.StringFlag{Name: "file", Required: true, Usage: "JSON object: parameter map {name:{kind:plain,value:string}} or CORS origins/methods/headers/expose_headers"}}, output, diagnostics, func(ctx context.Context, cmd *cli.Command, c *functions.Client, i authclient.Instance) (apppublish.Result, error) {
			raw, err := os.ReadFile(cmd.String("file"))
			if err != nil {
				return apppublish.Result{}, fmt.Errorf("read configuration file: %w", err)
			}
			var object map[string]json.RawMessage
			if json.Unmarshal(raw, &object) != nil || object == nil {
				return apppublish.Result{}, errors.New("configuration file must contain a JSON object")
			}
			var result any
			err = c.Request(ctx, "PUT", functions.Path(i.ID)+"/"+kind, map[string]any{kind: json.RawMessage(raw)}, &result)
			return apppublish.Success(result), err
		}),
	}}
}

func newFunctionEnabledCommand(enabled bool, output, diagnostics io.Writer) *cli.Command {
	name := "disable"
	if enabled {
		name = "enable"
	}
	return functionCommand(name, strings.Title(name)+" function access", nil, output, diagnostics, func(ctx context.Context, _ *cli.Command, c *functions.Client, i authclient.Instance) (apppublish.Result, error) {
		var result any
		err := c.Request(ctx, "POST", functions.InstancePath(i.ID)+"/enabled", map[string]bool{"enabled": enabled}, &result)
		return apppublish.Result{Status: "accepted", Data: result}, err
	})
}

func newFunctionPublishCommand(output, diagnostics io.Writer) *cli.Command {
	return functionCommand("publish", "Compile saved main-branch source and publish the resulting artifact", []cli.Flag{createWaitOption(), &cli.StringFlag{Name: "commit", Usage: "Saved source commit; omit to read main"}, &cli.StringFlag{Name: "request-id", Usage: "Reuse the original request ID after an uncertain response"}}, output, diagnostics, func(ctx context.Context, cmd *cli.Command, c *functions.Client, i authclient.Instance) (apppublish.Result, error) {
		commit := cmd.String("commit")
		if commit == "" {
			source, err := c.Source(ctx, i.ID)
			if err != nil {
				return apppublish.Result{}, err
			}
			defer source.Close()
			view, err := source.Select(ctx, "")
			if err != nil {
				return apppublish.Result{}, err
			}
			commit = view.Commit
			if view.Sample {
				return apppublish.Result{}, errors.New("save function source to Git before publishing")
			}
		}
		if cmd.String("request-id") == "" {
			var release struct {
				Published *struct {
					BuildCommit string `json:"build_commit"`
				} `json:"published"`
			}
			if err := c.Request(ctx, "GET", functions.Path(i.ID)+"/release", nil, &release); err != nil {
				return apppublish.Result{}, err
			}
			if release.Published != nil && release.Published.BuildCommit == commit {
				return apppublish.Success(map[string]any{"instance_id": i.ID, "already_published": true, "source_commit": commit, "release": release}), nil
			}
		}
		key := cmd.String("request-id")
		if key == "" {
			var err error
			key, err = authclient.NewIdempotencyKey()
			if err != nil {
				return apppublish.Result{}, err
			}
		}
		if _, err := fmt.Fprintf(diagnostics, "Build request: %s (source commit %s)\n", safeDisplay(key), safeDisplay(commit)); err != nil {
			return apppublish.Result{}, err
		}
		var build functions.Submission
		err := c.Request(ctx, "POST", functions.Path(i.ID)+"/builds", map[string]string{"source_commit": commit, "request_id": key}, &build)
		result := apppublish.Result{Status: "accepted", Data: map[string]any{"instance_id": i.ID, "request_id": key, "source_commit": commit, "build": build}}
		if err != nil {
			result.Status = "unknown"
			return result, err
		}
		if build.ID == "" || build.Sequence == "" {
			return result, errors.New("build receipt is incomplete; inspect builds before retrying with the same request ID and commit")
		}
		for cmd.Bool("wait") {
			done, err := build.Terminal()
			if done {
				result.Status = "succeeded"
				if err != nil {
					result.Status = "failed"
				}
				return result, err
			}
			if err := waitFunctionPoll(ctx); err != nil {
				return result, err
			}
			build, err = c.Build(ctx, i.ID, build)
			result.Data = map[string]any{"instance_id": i.ID, "request_id": key, "source_commit": commit, "build": build}
			if err != nil {
				return result, err
			}
		}
		return result, nil
	})
}

func waitFunctionPoll(ctx context.Context) error {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func newFunctionDeleteCommand(input io.Reader, output, diagnostics io.Writer) *cli.Command {
	return functionCommand("delete", "Delete a function and its artifacts; keep the source Git", []cli.Flag{deleteWaitOption(), &cli.BoolFlag{Name: "force", Aliases: []string{"f"}, Usage: "Skip confirmation"}, &cli.StringFlag{Name: "request-id", Usage: "Reuse the original deletion request ID"}}, output, diagnostics, func(ctx context.Context, cmd *cli.Command, c *functions.Client, i authclient.Instance) (apppublish.Result, error) {
		if !cmd.Bool("force") {
			if cmd.Bool("json") || !isTerminal(input) {
				return apppublish.Result{}, errors.New("deletion requires terminal confirmation or --force")
			}
			fmt.Fprintf(diagnostics, "Delete function %q (%s) and its artifacts? Source Git is kept. [y/N] ", safeDisplay(i.DisplayName), safeDisplay(i.ID))
			accepted, err := readDeleteConfirmation(ctx, input.(*os.File))
			if err != nil {
				return apppublish.Result{}, err
			}
			if !accepted {
				return apppublish.Result{Status: "cancelled"}, nil
			}
		}
		if i.ProductState == "DELETED" {
			return apppublish.Success(i), nil
		}
		key := cmd.String("request-id")
		if key == "" {
			var err error
			key, err = authclient.NewIdempotencyKey()
			if err != nil {
				return apppublish.Result{}, err
			}
		}
		if _, err := fmt.Fprintf(diagnostics, "Deletion request: %s (instance %s)\n", safeDisplay(key), safeDisplay(i.ID)); err != nil {
			return apppublish.Result{}, err
		}
		var receipt struct {
			authclient.InstanceDeletionReceipt
			JobID uint64 `json:"job_id"`
		}
		err := c.Request(ctx, "DELETE", functions.InstancePath(i.ID), map[string]any{"request_id": key, "delete_git": false}, &receipt)
		result := apppublish.Result{Status: "accepted", Data: map[string]any{"instance_id": i.ID, "request_id": key, "receipt": receipt}}
		if err != nil {
			return result, err
		}
		for cmd.Bool("wait") {
			if err := waitFunctionPoll(ctx); err != nil {
				return result, err
			}
			var current authclient.Instance
			if err := c.Request(ctx, "GET", functions.InstancePath(i.ID), nil, &current); err != nil {
				return result, err
			}
			if current.ProductState == "DELETED" {
				result.Status = "succeeded"
				return result, nil
			}
			if receipt.JobID != 0 {
				var job authclient.Job
				if err := c.Request(ctx, "GET", "/api/v1/jobs/"+strconv.FormatUint(receipt.JobID, 10), nil, &job); err != nil {
					return result, err
				}
				if job.Status == "fail" {
					result.Status = "failed"
					result.Error = &apppublish.Error{Code: "FUNCTION_DELETE_FAILED", Message: "Function deletion failed", NextAction: "Inspect and retry the deletion job", ExitCode: 1}
					return result, nil
				}
			}
		}
		return result, nil
	})
}
