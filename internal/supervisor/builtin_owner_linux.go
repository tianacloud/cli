//go:build linux

package supervisor

import (
	"context"
	"golang.org/x/sys/unix"
)

func watchBuiltinOwner(ctx context.Context, id ChildIdentity) (<-chan struct{}, error) {
	actual, err := newChildIdentity(id.PID)
	if err != nil || id.StartTime == 0 || actual != id {
		return nil, ErrHelperProtocol
	}
	fd, err := unix.PidfdOpen(id.PID, unix.PIDFD_NONBLOCK)
	if err != nil {
		return nil, ErrHelperProtocol
	}
	actual, err = newChildIdentity(id.PID)
	if err != nil || actual != id {
		unix.Close(fd)
		return nil, ErrHelperProtocol
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer unix.Close(fd)
		for ctx.Err() == nil {
			p := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			n, err := unix.Poll(p, 100)
			if err == unix.EINTR {
				continue
			}
			if err != nil || n > 0 {
				return
			}
		}
	}()
	return done, nil
}
