//go:build windows

package supervisor

import "os/exec"

func prepareNativeTTY(_ IO, _ *exec.Cmd) (func(), error) {
	return func() {}, nil
}

func isTerminalFD(uintptr) bool { return true }
