//go:build !linux && !darwin && !windows

package webtemplate

import "errors"

func renameProject(from, to string) error {
	return errors.New("atomic project creation is unavailable on this platform")
}
