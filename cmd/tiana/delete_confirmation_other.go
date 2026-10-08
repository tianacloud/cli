//go:build !linux && !darwin && !windows

package main

import (
	"context"
	"errors"
	"os"
)

func readDeleteConfirmation(context.Context, *os.File) (bool, error) {
	return false, errors.New("terminal confirmation unsupported; use --force")
}
