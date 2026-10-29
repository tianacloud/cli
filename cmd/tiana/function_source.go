package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/functions"
	"github.com/urfave/cli/v3"
)

func newFunctionSourceCommand(output, diagnostics io.Writer) *cli.Command {
	group := &cli.Command{Name: "source", Usage: "Read and save main-branch function source directly in Git", Action: groupAction}
	for _, name := range []string{"list", "read", "save"} {
		name := name
		usage := map[string]string{"list": "List one directory page", "read": "Read a source file (raw bytes without --json)", "save": "Save changed UTF-8 files in one Git commit"}[name]
		flags := []cli.Flag{functionJSONFlag()}
		if name == "save" {
			flags = append(flags, &cli.StringFlag{Name: "base-commit", Required: true, Usage: "Commit previously read; use empty for the first commit"}, &cli.StringSliceFlag{Name: "file", Required: true, Usage: "PATH=LOCAL_FILE relative to the function directory; repeat for each changed file"})
		} else {
			flags = append(flags, &cli.StringFlag{Name: "commit", Usage: "Commit to read; omit for current main"})
			if name == "list" {
				flags = append(flags, &cli.IntFlag{Name: "offset", Usage: "Directory offset from next_offset"})
			}
		}
		group.Commands = append(group.Commands, &cli.Command{Name: name, Usage: usage, ArgsUsage: "INSTANCE [PATH]", Flags: flags, Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() < 1 || cmd.NArg() > 2 || name == "read" && cmd.NArg() != 2 || name == "save" && cmd.NArg() != 1 {
				return argumentFailure(ctx, cmd, "provide INSTANCE and a source path for read; list accepts an optional directory")
			}
			if name == "list" && cmd.Int("offset") < 0 {
				return argumentFailure(ctx, cmd, "offset must be non-negative")
			}
			// Read local edits before acquiring credentials or contacting Git.
			files := map[string]string{}
			if name == "save" {
				for _, value := range cmd.StringSlice("file") {
					remote, local, ok := strings.Cut(value, "=")
					if !ok || remote == "" || local == "" {
						return argumentFailure(ctx, cmd, "--file must be PATH=LOCAL_FILE")
					}
					if _, err := functions.SourcePath("", remote); err != nil {
						return argumentFailure(ctx, cmd, err.Error())
					}
					raw, err := os.ReadFile(local)
					if err != nil {
						writeCommandError(diagnostics, fmt.Errorf("read source file: %w", err))
						return statusError(1)
					}
					if !utf8.Valid(raw) {
						return argumentFailure(ctx, cmd, "source save accepts UTF-8 files; use native Git for binary files")
					}
					files[remote] = string(raw)
				}
			}
			var rawOutput []byte
			// The ordinary result envelope remains available for agents; raw reads
			// can be redirected to a file and edited without embedding source in JSON.
			resultOutput := output
			if name == "read" && !cmd.Bool("json") {
				resultOutput = io.Discard
			}
			err := runFunctionCommand(ctx, cmd, resultOutput, diagnostics, func(c *functions.Client) (apppublish.Result, error) {
				instance, err := c.Resolve(ctx, cmd.Args().First())
				if err != nil {
					return apppublish.Result{}, err
				}
				source, err := c.Source(ctx, instance.ID)
				if err != nil {
					return apppublish.Result{}, err
				}
				defer source.Close()
				commit := cmd.String("commit")
				if name == "save" {
					commit = cmd.String("base-commit")
					if commit == "empty" {
						commit = ""
					}
				}
				view, err := source.Select(ctx, commit)
				if err != nil {
					return apppublish.Result{}, err
				}
				switch name {
				case "list":
					if !view.Sample && (cmd.Args().Get(1) != "" || cmd.Int("offset") != 0) {
						directory, err := functions.SourcePath(view.Root, cmd.Args().Get(1))
						if err != nil {
							return apppublish.Result{}, err
						}
						tree, err := source.Tree(ctx, view.Commit, directory, cmd.Int("offset"))
						if err != nil {
							return apppublish.Result{}, err
						}
						view.Entries, view.NextOffset = tree.Entries, tree.NextOffset
					}
					return apppublish.Success(view), nil
				case "read":
					if view.Sample {
						return apppublish.Result{}, errors.New("function source has not been saved yet")
					}
					rawOutput, err = source.Read(ctx, view, cmd.Args().Get(1))
					if cmd.Bool("json") && !utf8.Valid(rawOutput) {
						return apppublish.Result{}, errors.New("binary source cannot be represented as JSON text; omit --json to read raw bytes")
					}
					return apppublish.Success(map[string]any{"commit": view.Commit, "root": view.Root, "path": cmd.Args().Get(1), "content": string(rawOutput)}), err
				default:
					if cmd.String("base-commit") == "empty" && view.Commit != "" {
						return apppublish.Result{}, errors.New("repository already has a commit; read main and use its commit before saving")
					}
					commit, err := source.Save(ctx, view, files, instance.DisplayName)
					return apppublish.Success(map[string]string{"commit": commit, "root": view.Root}), err
				}
			})
			if err == nil && name == "read" && !cmd.Bool("json") {
				if _, err := output.Write(rawOutput); err != nil {
					return err
				}
			}
			return err
		}})
	}
	return group
}
