//go:build windows

package supervisor

import (
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func configureNativeProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}
func configureHelperProcess(command *exec.Cmd) { configureNativeProcess(command) }
func forwardNativeSignal(command *exec.Cmd, value os.Signal) {
	if command != nil && command.Process != nil {
		if value == os.Interrupt {
			// CTRL_C_EVENT cannot target a process group. CTRL_BREAK_EVENT reaches
			// this native child and its console descendants without interrupting us.
			_ = windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(command.Process.Pid))
		} else {
			_ = command.Process.Signal(value)
		}
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
