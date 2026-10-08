//go:build !linux && !darwin && !windows

package sqlitecli

import "errors"

func readTokenFile(string) ([]byte, error) {
	return nil, errors.New("Token files unsupported on this platform")
}
