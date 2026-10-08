//go:build linux || darwin

package authclient

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Acquire prevents cooperating CLI invocations from replacing each other's
// pending intent between Load and the final Save/Delete. The persistent lock
// inode is never unlinked; closing the descriptor releases it on crash or exit.
// Contention fails locally without waiting or issuing a remote write.
func (s *FilePendingCommandStore) Acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.Path == "" {
		return nil, errors.New("pending command store is not configured")
	}
	if err := preparePendingDir(filepath.Dir(s.Path)); err != nil {
		return nil, err
	}
	fd, err := unix.Open(s.Path+".lock", unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, errors.New("cannot open pending command lock")
	}
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&07777 != 0600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		_ = unix.Close(fd)
		return nil, errors.New("unsafe pending command lock")
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = unix.Close(fd)
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, errors.New("another CLI command is using this pending store; wait for it to finish and retry")
		}
		return nil, errors.New("cannot lock pending command store")
	}
	if err := ctx.Err(); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	file := os.NewFile(uintptr(fd), s.Path+".lock")
	return func() { _ = file.Close() }, nil
}

// Match the existing private-directory permission policy, but validate ownership
// and restrict the opened directory rather than following a path in Chmod.
func preparePendingDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return errors.New("cannot prepare pending command directory")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return errors.New("pending command directory must not be a symlink")
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uint32(os.Geteuid()) {
		return errors.New("pending command directory must belong to the current user")
	}
	if unix.Fchmod(fd, 0700) != nil {
		return errors.New("cannot restrict pending command directory")
	}
	return nil
}
