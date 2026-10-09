//go:build windows

package authclient

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/tianacloud/cli/internal/localfile"
	"github.com/tianacloud/cli/internal/localstate"
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
		return nil, &localstate.Error{Code: "LOCAL_STATE_BUSY", Operation: "lock pending command", Path: s.Path + ".lock", Cause: err}
	}
	return release, localstate.Wrap("open pending command lock", s.Path+".lock", err)
}

func preparePendingDir(path string) error {
	return localstate.Wrap("prepare private pending command directory", path, localfile.PrivateDir(path))
}
