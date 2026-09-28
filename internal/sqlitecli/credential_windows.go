//go:build windows

package sqlitecli

import "github.com/tianacloud/cli/internal/localfile"

func readTokenFile(path string) ([]byte, error) {
	return localfile.Read(path, 128, true)
}
