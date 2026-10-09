package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/supervisor"
	"github.com/tianacloud/cli/internal/updatecheck"
	"github.com/urfave/cli/v3"
)

const updateWorkerArgument = "__tiana_update_check"

func printCurrentVersion(output io.Writer) error {
	_, err := fmt.Fprintf(output, "tiana %s (helper-contract %d)\n", version, supervisor.HelperContractVersion)
	return err
}
func newVersionCommand(output, diagnostics io.Writer, checker updatecheck.Checker) *cli.Command {
	return &cli.Command{Name: "version", Usage: "Print version or check package updates", Flags: []cli.Flag{boolOption("json", "Write a structured JSON result")}, Action: func(ctx context.Context, cmd *cli.Command) error {
		if cmd.NArg() != 0 {
			return argumentFailure(ctx, cmd, "version does not accept arguments")
		}
		if managementJSON(ctx) {
			return statusError(writeAppResult(apppublish.Success(map[string]any{"version": version, "helper_contract": supervisor.HelperContractVersion}), true, output, diagnostics))
		}
		if printCurrentVersion(output) != nil {
			return statusError(1)
		}
		return nil
	}, Commands: []*cli.Command{{Name: "check", Usage: "Check known Skills and CLI updates; share the daily reminder allowance", Flags: []cli.Flag{stringOption("skills-version", "Version of the currently loaded Skills", ""), boolOption("json", "Write the update check result as JSON")}, Action: func(ctx context.Context, cmd *cli.Command) error {
		if cmd.NArg() != 0 {
			return argumentFailure(ctx, cmd, "version check does not accept positional arguments")
		}
		versions := map[string]string{updatecheck.CLIPackage: version}
		if skills := cmd.String("skills-version"); skills != "" {
			versions[updatecheck.SkillsPackage] = skills
		}
		result := checker.Check(ctx, versions, updatecheck.Options{Fetch: true, Notify: true})
		if cmd.Bool("json") {
			if json.NewEncoder(output).Encode(result) != nil {
				return statusError(1)
			}
		} else {
			if result.ShouldNotify {
				fmt.Fprintln(output, updatecheck.Message(result.Updates))
			} else {
				fmt.Fprintf(output, "Update check: %s\n", result.CheckStatus)
				for _, item := range result.Updates {
					fmt.Fprintf(output, "%s: %s → %s\n", item.Package, item.CurrentVersion, item.LatestVersion)
				}
			}
		}
		return nil
	}}}}
}
func passiveUpdateAllowed(args []string) bool {
	command := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--json" || strings.HasPrefix(arg, "--json=") || arg == "--help" || arg == "-h" || (command == "" && (arg == "--version" || arg == "-v" || strings.HasPrefix(arg, "--version="))) {
			return false
		}
		if arg == "--ca-file" {
			i++
			continue
		}
		if strings.HasPrefix(arg, "--ca-file=") {
			continue
		}
		if command == "" && !strings.HasPrefix(arg, "-") {
			command = arg
		}
	}
	if command == "" || command == "version" || command == "connect" || command == updateWorkerArgument {
		return false
	}
	if command == "git" {
		for _, arg := range args {
			if arg == "remote-helper" {
				return false
			}
		}
	}
	return true
}
func passiveUpdate(ctx context.Context, args []string, input io.Reader, output, diagnostics io.Writer) {
	if !isTerminal(input) || !isTerminal(output) || !isTerminal(diagnostics) || !passiveUpdateAllowed(args) {
		return
	}
	result := (updatecheck.Checker{}).Check(ctx, map[string]string{updatecheck.CLIPackage: version}, updatecheck.Options{Notify: true})
	if result.ShouldNotify {
		fmt.Fprintln(diagnostics, updatecheck.Message(result.Updates))
	}
	if !result.RefreshDue || ctx.Err() != nil {
		return
	}
	executable, err := os.Executable()
	if err != nil {
		return
	}
	worker := exec.Command(executable, updateWorkerArgument)
	if worker.Start() == nil {
		_ = worker.Process.Release()
	}
}
