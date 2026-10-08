//go:build windows

package supervisor

import "github.com/tianacloud/cli/internal/localfile"

func readTokenFileSecure(path string) ([]byte, error) {
	return localfile.Read(path, maxTokenIn, true)
}
