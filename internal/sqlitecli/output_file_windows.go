//go:build windows

package sqlitecli

import (
	"github.com/tianacloud/cli/internal/localfile"
	"os"
)

// CreateOutput exclusively creates a SQL output file with an owner-only ACL.
func CreateOutput(path string) (*os.File, error) {
	return localfile.CreateExclusive(path)
}
