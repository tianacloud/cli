//go:build !linux && !darwin && !windows

package gitremote

import "errors"

func readRegular(string, int64, bool) ([]byte, error) {
	return nil, errors.New("Git credential and CA files require Linux or macOS")
}
