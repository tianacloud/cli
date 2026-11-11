// Package gitcommand invokes native Git using the helper bundled with this CLI.
package gitcommand

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tianacloud/cli/internal/clientconfig"
	"github.com/tianacloud/cli/internal/supervisor"
	"github.com/tianacloud/cli/internal/transportmode"
)

func Run(ctx context.Context, args []string, streams supervisor.IO) (int, error) {
	executable, err := os.Executable()
	if err != nil {
		return 1, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return 1, err
	}
	command, err := prepare(ctx, executable, args)
	if err != nil {
		return 1, err
	}
	return supervisor.RunGitProcess(ctx, command, streams)
}

func prepare(ctx context.Context, executable string, args []string) (*exec.Cmd, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return nil, errors.New("Git is required; install Git and retry")
	}
	directory := filepath.Dir(executable)
	helper := filepath.Join(directory, "git-remote-tiana")
	info, err := os.Stat(helper)
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode()&0111 == 0) {
		return nil, errors.New("bundled Git helper is missing; reinstall this Tiana CLI package")
	}
	output, err := exec.CommandContext(ctx, git, "--exec-path").Output()
	if err != nil {
		return nil, errors.New("cannot locate Git executable directory")
	}
	for _, name := range []string{"git-remote-tiana", "git-remote-tiana.exe"} {
		candidate := filepath.Join(strings.TrimSpace(string(output)), name)
		if found, err := os.Stat(candidate); err == nil && !os.SameFile(info, found) {
			return nil, errors.New("Git executable directory contains a conflicting Tiana helper; remove that installation before retrying")
		}
	}
	arguments := append([]string{"-c", "tiana.transport=" + transportmode.Name(transportmode.FromContext(ctx))}, args...)
	command := exec.Command(git, arguments...)
	command.Env = replaceEnv(os.Environ(), "PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	if trust := clientconfig.FromContext(ctx); trust != nil && trust.CAFile != "" {
		absolute, err := filepath.Abs(trust.CAFile)
		if err != nil {
			return nil, err
		}
		command.Env = replaceEnv(command.Env, "TIANA_CA_FILE", absolute)
	}
	return command, nil
}

func replaceEnv(environment []string, key, value string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		name, _, _ := strings.Cut(item, "=")
		if name == key || (runtime.GOOS == "windows" && strings.EqualFold(name, key)) {
			continue
		}
		result = append(result, item)
	}
	return append(result, key+"="+value)
}
