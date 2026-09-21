//go:build !linux && !darwin

package supervisor

import (
	"context"
	"os/exec"
)

func embeddedHelperCommand(context.Context, []byte) (*exec.Cmd, func(), error) {
	return nil, nil, ErrHelperNotTrusted
}
