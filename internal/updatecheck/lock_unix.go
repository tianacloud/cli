//go:build linux || darwin

package updatecheck

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func tryLock(ctx context.Context, path string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		unix.Close(fd)
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, errBusy
		}
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	return func() { file.Close() }, nil
}
