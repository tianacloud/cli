//go:build darwin

package supervisor

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"syscall"
)

// readTokenFileSecure opens first, refuses a final symlink, then validates
// ownership and exact mode on the descriptor whose bytes are consumed.
func readTokenFileSecure(path string) ([]byte, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("read credential file: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = syscall.Close(fd)
		return nil, ErrCredentialPermissions
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || !ownedByCurrentUser(info) {
		return nil, ErrCredentialPermissions
	}
	read, err := io.ReadAll(io.LimitReader(bufio.NewReader(file), maxTokenIn+1))
	if err != nil {
		zeroBytes(read)
		return nil, fmt.Errorf("read credential file: %w", err)
	}
	if len(read) > maxTokenIn {
		zeroBytes(read)
		return nil, fmt.Errorf("credential file is too large")
	}
	return read, nil
}
