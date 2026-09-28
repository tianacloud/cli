//go:build windows

package authclient

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/tianacloud/cli/internal/localfile"
	"golang.org/x/sys/windows"
)

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
	release, err := localfile.TryLock(ctx, s.Path+".lock")
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return nil, errors.New("another CLI command is using this pending store; wait for it to finish and retry")
	}
	return release, err
}

func preparePendingDir(path string) error { return localfile.PrivateDir(path) }
