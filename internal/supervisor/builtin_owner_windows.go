//go:build windows

package supervisor

import (
	"context"
	"golang.org/x/sys/windows"
)

func watchBuiltinOwner(ctx context.Context, id ChildIdentity) (<-chan struct{}, error) {
	if id.PID <= 0 || id.StartTime == 0 {
		return nil, ErrHelperProtocol
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(id.PID))
	if err != nil {
		return nil, ErrHelperProtocol
	}
	// Read identity from the same handle retained for exit observation, so PID
	// reuse cannot switch the observed process after the identity check.
	actual, err := windowsChildIdentity(handle, id.PID)
	if err != nil || actual != id {
		windows.CloseHandle(handle)
		return nil, ErrHelperProtocol
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer windows.CloseHandle(handle)
		for ctx.Err() == nil {
			status, err := windows.WaitForSingleObject(handle, 100)
			if err != nil || status != uint32(windows.WAIT_TIMEOUT) {
				return
			}
		}
	}()
	return done, nil
}
