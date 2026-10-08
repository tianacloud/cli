//go:build linux || darwin

package main

import (
	"context"
	"errors"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// Polling keeps Ctrl-C cancellable without a blocked reader goroutine or taking
// ownership of stdin. Bound input and require a complete affirmative line.
func readDeleteConfirmation(ctx context.Context, input *os.File) (bool, error) {
	fd := int(input.Fd())
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		return false, err
	}
	if err = unix.SetNonblock(fd, true); err != nil {
		return false, err
	}
	defer unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags)
	var line strings.Builder
	var one [1]byte
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		events := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(events, 100)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return false, err
		}
		if n == 0 {
			continue
		}
		n, err = unix.Read(fd, one[:])
		if err == unix.EAGAIN || err == unix.EINTR {
			continue
		}
		if err != nil {
			return false, err
		}
		if n == 0 {
			return false, nil
		}
		if one[0] == '\n' {
			answer := strings.ToLower(strings.TrimSpace(line.String()))
			return answer == "y" || answer == "yes", nil
		}
		if line.Len() >= 64 {
			return false, errors.New("confirmation input exceeds limit")
		}
		line.WriteByte(one[0])
	}
}
