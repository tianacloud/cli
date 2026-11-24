//go:build !linux && !darwin && !windows

package apppublish

import "errors"

func renameNoReplace(from, to string) error {
	return errors.New("atomic directory replacement is unavailable on this platform")
}
