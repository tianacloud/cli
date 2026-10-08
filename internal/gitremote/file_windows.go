//go:build windows

package gitremote

import "github.com/tianacloud/cli/internal/localfile"

func readRegular(path string, limit int64, private bool) ([]byte, error) {
	if private {
		return localfile.Read(path, limit, true)
	}
	return localfile.ReadFollow(path, limit)
}
