//go:build !linux && !darwin && !windows

package supervisor

import "errors"

func readTokenFileSecure(string) ([]byte, error) {
	// The portable fallback cannot prove no-follow/open-first semantics or ACL
	// ownership, so refusing the source is safer than silently widening it.
	return nil, errors.New("credential files are unsupported on this platform")
}
