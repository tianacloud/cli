//go:build !linux && !darwin && !windows

package supervisor

func newChildIdentity(_ int) (ChildIdentity, error) {
	// No equivalent stable owner identity is frozen for these platforms yet.
	return ChildIdentity{}, ErrHelperProtocol
}
