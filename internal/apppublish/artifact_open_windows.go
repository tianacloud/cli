//go:build windows

package apppublish

import (
	"errors"
	"os"
)

func openArtifact(root *os.Root, path string) (*os.File, error) {
	info, err := root.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("build output contains a non-regular file")
	}
	return root.Open(path)
}
