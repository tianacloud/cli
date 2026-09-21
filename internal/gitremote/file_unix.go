//go:build linux || darwin

package gitremote

import (
	"errors"
	"os"
	"syscall"
)

func readRegular(path string, limit int64, private bool) ([]byte, error) {
	flags := syscall.O_RDONLY | syscall.O_CLOEXEC | syscall.O_NONBLOCK
	if private {
		flags |= syscall.O_NOFOLLOW
	}
	fd, err := syscall.Open(path, flags, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Size() > limit || (private && (!ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm() != 0600)) {
		return nil, errors.New("invalid file")
	}
	b, err := readBounded(f, limit)
	if int64(len(b)) > limit {
		clear(b)
		return nil, errors.New("file exceeds limit")
	}
	return b, err
}
