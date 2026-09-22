//go:build darwin

package main

import (
	"bytes"
	"golang.org/x/sys/unix"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

func openDeleteTestPTY(t *testing.T) (*os.File, *os.File) {
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
