//go:build darwin

package supervisor

import (
	"bytes"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const ttyHandoffTestRole = "TIANA_TTY_HANDOFF_TEST_ROLE"

func TestDevNullIsNotMisclassifiedAsInteractiveTerminal(t *testing.T) {
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if isTTYReader(file) {
		t.Fatal("character device without terminal ioctls was classified as a TTY")
	}
}

func TestHandoffNativeTTYChangesAndRestoresForegroundGroup(t *testing.T) {
	switch os.Getenv(ttyHandoffTestRole) {
	case "helper":
		runTTYHandoffHelper(t)
		return
	case "child":
		time.Sleep(30 * time.Second)
		return
	}

	master, slave := openTestPTY(t)
	defer master.Close()
	defer slave.Close()

	command := exec.Command(os.Args[0], "-test.run=^TestHandoffNativeTTYChangesAndRestoresForegroundGroup$")
	command.Env = append(os.Environ(), ttyHandoffTestRole+"=helper")
	command.Stdin = slave
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	command.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	select {
	case err := <-wait:
		if err != nil {
			t.Fatalf("TTY handoff helper failed: %v\n%s", err, output.String())
		}
	case <-time.After(2 * time.Second):
		_ = command.Process.Kill()
		<-wait
		t.Fatalf("TTY handoff helper did not exit; it may have been stopped by SIGTTOU\n%s", output.String())
	}
}

func runTTYHandoffHelper(t *testing.T) {
	// Match an interactive shell's child disposition. Restoring the terminal
	// while this process is in the background must not stop it with SIGTTOU.
	signal.Reset(syscall.SIGTTOU)

	previous := readTerminalPgrp(t, os.Stdin)
	if want := int32(syscall.Getpgrp()); previous != want {
		t.Fatalf("initial foreground group = %d, want helper group %d", previous, want)
	}

	child := exec.Command(os.Args[0], "-test.run=^TestHandoffNativeTTYChangesAndRestoresForegroundGroup$")
	child.Env = append(os.Environ(), ttyHandoffTestRole+"=child")
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	configureNativeProcess(child)
	restore, err := prepareNativeTTY(IO{Stdin: os.Stdin}, child)
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		restore()
		t.Fatal(err)
	}
	defer func() {
		_ = child.Process.Kill()
		_, _ = child.Process.Wait()
	}()

	if got, want := readTerminalPgrp(t, os.Stdin), int32(child.Process.Pid); got != want {
		t.Fatalf("foreground group after handoff = %d, want child group %d", got, want)
	}

	restore()
	if got := readTerminalPgrp(t, os.Stdin); got != previous {
		t.Fatalf("foreground group after restore = %d, want original group %d", got, previous)
	}
}

func readTerminalPgrp(t *testing.T, terminal *os.File) int32 {
	t.Helper()
	var pgrp int32
	if err := terminalPgrp(terminal, syscall.TIOCGPGRP, &pgrp); err != nil {
		t.Fatal(err)
	}
	return pgrp
}

func openTestPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	masterFD, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	master := os.NewFile(uintptr(masterFD), "/dev/ptmx")
	for _, request := range []uintptr{unix.TIOCPTYGRANT, unix.TIOCPTYUNLK} {
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(masterFD), request, 0); errno != 0 {
			master.Close()
			t.Fatal(errno)
		}
	}
	var name [128]byte
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(masterFD), unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0]))); errno != 0 {
		master.Close()
		t.Fatal(errno)
	}
	slavePath := string(bytes.TrimRight(name[:], "\x00"))
	slave, err := os.OpenFile(slavePath, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		t.Fatal(err)
	}
	return master, slave
}
