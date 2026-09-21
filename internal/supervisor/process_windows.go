//go:build windows

package supervisor

import (
	"os"
	"os/exec"
	"os/signal"
)

func configureNativeProcess(_ *exec.Cmd) {}
func configureHelperProcess(_ *exec.Cmd) {}
func forwardNativeSignal(command *exec.Cmd, value os.Signal) {
	if command != nil && command.Process != nil {
		_ = command.Process.Signal(value)
	}
}
func nativeExitCode(command *exec.Cmd, _ error) int {
	if command != nil && command.ProcessState != nil && command.ProcessState.ExitCode() >= 0 {
		return command.ProcessState.ExitCode()
	}
	return 1
}
func watchSignals() (<-chan os.Signal, func()) {
	channel := make(chan os.Signal, 2)
	signal.Notify(channel, os.Interrupt)
	return channel, func() { signal.Stop(channel) }
}
func isResizeSignal(_ os.Signal) bool { return false }
