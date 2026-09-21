//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package supervisor

import (
	"os"
	"syscall"
)

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return uint64(stat.Uid) == uint64(os.Getuid())
}

func trustedOwner(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	// The Unix MVP only trusts a platform-managed installation boundary.
	// User-owned trees remain replaceable even when their mode bits are
	// read-only, so they cannot establish a stable hash-to-exec identity.
	return uint64(stat.Uid) == 0
}

// trustedWritable accepts only root-owned objects with no group/other write
// bit. The caller may then re-open the no-follow regular file and hash that
// verified object immediately before the absolute-path exec.
func trustedWritable(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return uint64(stat.Uid) == 0 && info.Mode().Perm()&0o022 == 0
}
