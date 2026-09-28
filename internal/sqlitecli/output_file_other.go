//go:build !windows

package sqlitecli

import "os"

// CreateOutput exclusively creates a private SQL output file.
func CreateOutput(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
}
