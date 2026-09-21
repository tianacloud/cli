//go:build linux

package supervisor

import (
	"fmt"
	"os"
	"syscall"
)

func openTrustedRegular(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("invalid trusted file")
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !trustedOwner(info) || !trustedWritable(info) {
		_ = file.Close()
		return nil, ErrHelperNotTrusted
	}
	return file, nil
}
