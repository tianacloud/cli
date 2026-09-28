//go:build windows

package sqlitecli

import "github.com/tianacloud/cli/internal/localfile"

// ReadFile admits bounded regular SQL/CA files, including symlinks.
func ReadFile(path string, limit int) ([]byte, error) {
	return localfile.ReadFollow(path, int64(limit))
}
