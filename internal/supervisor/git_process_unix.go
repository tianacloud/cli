//go:build !windows

package supervisor

import (
	"os/exec"
	"syscall"
)

type gitTree struct {
	command *exec.Cmd
	closed  bool
}

func startGitTree(command *exec.Cmd) (*gitTree, error) {
	if err := command.Start(); err != nil {
		return nil, err
	}
	return &gitTree{command: command}, nil
}
func (t *gitTree) interrupt() { _ = syscall.Kill(-t.command.Process.Pid, syscall.SIGINT) }
func (t *gitTree) close() {
	if !t.closed {
		_ = syscall.Kill(-t.command.Process.Pid, syscall.SIGKILL)
		t.closed = true
	}
}
