//go:build darwin

package supervisor

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

func embeddedHelperCommand(ctx context.Context, image []byte) (*exec.Cmd, func(), error) {
	directory, err := os.MkdirTemp("", "tiana-helper-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	if err := os.Chmod(directory, 0o700); err != nil {
		cleanup()
		return nil, nil, err
	}
	path := filepath.Join(directory, "tiana-helper")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o500)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	writeErr := writeAll(file, image)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		cleanup()
		return nil, nil, writeErr
	}
	if closeErr != nil {
		cleanup()
		return nil, nil, closeErr
	}
	opened, err := os.Open(path)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	digest := sha256.New()
	expectedDigest := sha256.Sum256(image)
	_, digestErr := io.Copy(digest, io.LimitReader(opened, maxEmbeddedHelperSize+1))
	stat, statErr := opened.Stat()
	closeErr = opened.Close()
	if digestErr != nil || statErr != nil || closeErr != nil || stat.Size() != int64(len(image)) || !stat.Mode().IsRegular() || stat.Mode().Perm() != 0o500 || subtle.ConstantTimeCompare(digest.Sum(nil), expectedDigest[:]) != 1 {
		cleanup()
		return nil, nil, fmt.Errorf("embedded helper staging verification failed")
	}
	return exec.CommandContext(ctx, path), cleanup, nil
}
