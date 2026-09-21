//go:build darwin

package supervisor

func newChildIdentity(pid int) (ChildIdentity, error) {
	if pid <= 0 {
		return ChildIdentity{}, ErrHelperProtocol
	}
	// macOS has no pidfd and no /proc starttime. The helper therefore treats
	// the private control pipe as its lifetime authority and requires this
	// explicit zero identity. This does not upgrade the loopback listener's
	// honest loopback_unisolated security claim.
	return ChildIdentity{PID: pid, StartTime: 0}, nil
}
