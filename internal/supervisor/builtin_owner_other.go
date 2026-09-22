//go:build !linux && !darwin

package supervisor

import "context"

func watchBuiltinOwner(context.Context, ChildIdentity) (<-chan struct{}, error) {
	return nil, ErrHelperProtocol
}
