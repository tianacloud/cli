//go:build !linux && !darwin

package sqlitecli

import "errors"

func readTokenFile(string) ([]byte, error) {
	return nil, errors.New("Token files unsupported on this platform")
}
