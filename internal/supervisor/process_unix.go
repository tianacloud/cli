//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package supervisor

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func configureNativeProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func configureHelperProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func forwardNativeSignal(command *exec.Cmd, value os.Signal) {
	if command == nil || command.Process == nil {
		return
	}
	signalValue, ok := value.(syscall.Signal)
	if !ok {
		return
	}
	if err := syscall.Kill(-command.Process.Pid, signalValue); err != nil {
		_ = command.Process.Signal(value)
	}
}

func nativeExitCode(command *exec.Cmd, err error) int {
	if err == nil {
		return 0
	}
	if command.ProcessState != nil && command.ProcessState.ExitCode() >= 0 {
		return command.ProcessState.ExitCode()
	}
	if command.ProcessState != nil {
		if status, ok := command.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal())
		}
	}
	return 1
}

func watchSignals() (<-chan os.Signal, func()) {
	channel := make(chan os.Signal, 8)
	signal.Notify(channel, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGWINCH)
	return channel, func() { signal.Stop(channel) }
}

func isResizeSignal(value os.Signal) bool { return value == syscall.SIGWINCH }
