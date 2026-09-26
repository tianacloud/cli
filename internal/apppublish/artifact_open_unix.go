//go:build !windows

package apppublish

import (
	"errors"
	"os"
	"syscall"
)

func openArtifact(root *os.Root, path string) (*os.File, error) {
	f, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("build output contains a non-regular file")
	}
	return f, nil
}
