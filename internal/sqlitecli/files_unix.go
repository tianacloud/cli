//go:build linux || darwin

package sqlitecli

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// ReadFile admits only bounded regular input, without blocking while opening
// a FIFO/device. Unlike credential files, scripts and CA files may be symlinks.
func ReadFile(path string, limit int) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > int64(limit) {
		return nil, errors.New("input must be a bounded regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil || len(b) > limit {
		return nil, errors.New("input unreadable or exceeds limit")
	}
	return b, nil
}
