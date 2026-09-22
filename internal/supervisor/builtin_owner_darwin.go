//go:build darwin

package supervisor

import (
	"context"
	"golang.org/x/sys/unix"
)

func watchBuiltinOwner(ctx context.Context, id ChildIdentity) (<-chan struct{}, error) {
	if id.PID <= 0 || id.StartTime != 0 {
		return nil, ErrHelperProtocol
	}
	fd, err := unix.Kqueue()
	if err != nil {
		return nil, ErrHelperProtocol
	}
	unix.CloseOnExec(fd)
	change := []unix.Kevent_t{{Ident: uint64(id.PID), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ENABLE | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT}}
	if _, err = unix.Kevent(fd, change, nil, nil); err != nil {
		unix.Close(fd)
		return nil, ErrHelperProtocol
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer unix.Close(fd)
		events := make([]unix.Kevent_t, 1)
		for ctx.Err() == nil {
			timeout := unix.NsecToTimespec(100000000)
			n, err := unix.Kevent(fd, nil, events, &timeout)
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
