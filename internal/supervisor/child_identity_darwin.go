//go:build darwin

package supervisor

func newChildIdentity(pid int) (ChildIdentity, error) {
	if pid <= 0 {
		return ChildIdentity{}, ErrHelperProtocol
	}
	// macOS has no pidfd or /proc starttime; contract 3 uses an explicit zero
	// identity. The built-in helper watches kqueue owner-exit notifications
	// in addition to the private control pipe. Neither mechanism upgrades
	// the listener's honest loopback_unisolated security claim.
	return ChildIdentity{PID: pid, StartTime: 0}, nil
}
