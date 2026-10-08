//go:build windows

package supervisor

import (
	"golang.org/x/term"
	"os/exec"
)

func prepareNativeTTY(_ IO, _ *exec.Cmd) (func(), error) {
	return func() {}, nil
}

func isTerminalFD(fd uintptr) bool { return term.IsTerminal(int(fd)) }
