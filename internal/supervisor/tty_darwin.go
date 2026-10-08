//go:build darwin

package supervisor

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"unsafe"
)

var terminalForegroundMu sync.Mutex

func isTerminalFD(fd uintptr) bool {
	var termios syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCGETA, uintptr(unsafe.Pointer(&termios)))
	return errno == 0
}

func prepareNativeTTY(streams IO, command *exec.Cmd) (func(), error) {
	noop := func() {}
	if command == nil {
		return noop, nil
	}
	terminal, ok := streams.Stdin.(*os.File)
	if !ok || !isTTYReader(terminal) {
		return noop, nil
	}

	var previous int32
	if err := terminalPgrp(terminal, syscall.TIOCGPGRP, &previous); err != nil {
		return noop, fmt.Errorf("read terminal foreground group: %w", err)
	}
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.Foreground = true
	command.SysProcAttr.Ctty = int(terminal.Fd())

	var once sync.Once
	return func() {
		once.Do(func() {
			_ = restoreTerminalPgrp(terminal, &previous)
		})
	}, nil
}

func restoreTerminalPgrp(terminal *os.File, pgrp *int32) error {
	terminalForegroundMu.Lock()
	defer terminalForegroundMu.Unlock()

	wasIgnored := signal.Ignored(syscall.SIGTTOU)
	if !wasIgnored {
		signal.Ignore(syscall.SIGTTOU)
		defer signal.Reset(syscall.SIGTTOU)
	}
	return terminalPgrp(terminal, syscall.TIOCSPGRP, pgrp)
}

func terminalPgrp(terminal *os.File, request uintptr, pgrp *int32) error {
	if terminal == nil || pgrp == nil {
		return io.ErrClosedPipe
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, terminal.Fd(), request, uintptr(unsafe.Pointer(pgrp)))
	if errno != 0 {
		return errno
	}
	return nil
}
