//go:build windows

package supervisor

import "golang.org/x/sys/windows"

func newChildIdentity(pid int) (ChildIdentity, error) {
	if pid <= 0 {
		return ChildIdentity{}, ErrHelperProtocol
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ChildIdentity{}, err
	}
	defer windows.CloseHandle(handle)
	return windowsChildIdentity(handle, pid)
}

func windowsChildIdentity(handle windows.Handle, pid int) (ChildIdentity, error) {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return ChildIdentity{}, err
	}
	return ChildIdentity{PID: pid, StartTime: uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)}, nil
}
