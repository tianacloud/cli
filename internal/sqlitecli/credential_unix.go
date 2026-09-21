//go:build linux || darwin

package sqlitecli

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func readTokenFile(path string) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&07777 != 0600 || stat.Uid != uint32(os.Getuid()) {
		return nil, errors.New("unsafe credential file")
	}
	b, err := io.ReadAll(io.LimitReader(f, 129))
	if err != nil || len(b) > 128 {
		clear(b)
		return nil, errors.New("invalid credential file")
	}
	return b, nil
}
