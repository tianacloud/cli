// Package localstate shares the SDK account directory and safe local diagnostics.
package localstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/tianacloud/sdk-go/auth"
)

func Directory() (string, error) {
	path, err := auth.DefaultCredentialPath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(path), nil
}

// Error reports only local operation/path and an allowlisted cause, never file data.
type Error struct {
	Code, Operation, Path string
	Cause                 error
}

func (e *Error) Error() string {
	reason := "local state unavailable"
	switch {
	case errors.Is(e.Cause, os.ErrPermission):
		reason = "permission denied"
	case errors.Is(e.Cause, syscall.ENOTDIR):
		reason = "not a directory"
	case errors.Is(e.Cause, syscall.EROFS):
		reason = "read-only filesystem"
	case errors.Is(e.Cause, syscall.ENOSPC):
		reason = "no space left on device"
	case e.Code == "LOCAL_STATE_BUSY":
		reason = "another CLI command is using this pending store; wait for it to finish and retry"
	case e.Code == "LEGACY_PENDING_COMMAND":
		reason = "unresolved command in previous directory; use TIANA_PENDING_COMMAND_FILE to recover the original command"
	}
	return fmt.Sprintf("%s: %s: %s", e.Operation, e.Path, reason)
}
func (e *Error) Unwrap() error { return e.Cause }
func Wrap(operation, path string, cause error) error {
	if cause == nil {
		return nil
	}
	var prior *Error
	if errors.As(cause, &prior) {
		return cause
	}
	return &Error{Code: "LOCAL_STATE_UNAVAILABLE", Operation: operation, Path: path, Cause: cause}
}
