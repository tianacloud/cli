//go:build windows

package updatecheck

import (
	"context"
	"errors"
	"github.com/tianacloud/cli/internal/localfile"
	"golang.org/x/sys/windows"
)

func tryLock(ctx context.Context, path string) (func(), error) {
	release, err := localfile.TryLock(ctx, path)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return nil, errBusy
	}
	return release, err
}
