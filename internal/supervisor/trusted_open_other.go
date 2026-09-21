//go:build !linux

package supervisor

import "os"

func openTrustedRegular(path string) (*os.File, error) {
	// Platforms without a portable no-follow open primitive fail closed rather
	// than reintroduce a check-then-open replacement window.
	return nil, os.ErrPermission
}
