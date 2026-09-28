//go:build !linux && !darwin && !windows

package sqlitecli

import "errors"

func ReadFile(string, int) ([]byte, error) {
	return nil, errors.New("file input unsupported on this platform")
}
