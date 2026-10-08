//go:build !linux && !darwin && !windows

package authclient

import (
	"context"
	"errors"
)

func (s *FilePendingCommandStore) Acquire(context.Context) (func(), error) {
	return nil, errors.New("secure pending command locking requires Linux or macOS")
}

func preparePendingDir(string) error {
	return errors.New("secure pending command files require Linux or macOS")
}
