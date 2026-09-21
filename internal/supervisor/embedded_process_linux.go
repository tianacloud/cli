//go:build linux

package supervisor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"

	"golang.org/x/sys/unix"
)

func embeddedHelperCommand(ctx context.Context, image []byte) (*exec.Cmd, func(), error) {
	fd, err := unix.MemfdCreate("tiana-helper", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, nil, err
	}
	file := os.NewFile(uintptr(fd), "tiana-helper")
	if file == nil {
		_ = unix.Close(fd)
		return nil, nil, fmt.Errorf("invalid helper memfd")
	}
	cleanup := func() { _ = file.Close() }
	if err := writeAll(file, image); err != nil {
		cleanup()
		return nil, nil, err
	}
	if err := unix.Fchmod(fd, 0o500); err != nil {
		cleanup()
		return nil, nil, err
	}
	seals := unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, seals); err != nil {
		cleanup()
		return nil, nil, err
	}
	if _, err := file.Seek(0, 0); err != nil {
		cleanup()
		return nil, nil, err
	}
	// ExtraFiles begin at descriptor 3 in the child. The descriptor remains
	// open across exec so /proc/self/fd/3 names the exact sealed image. The
	// parent's copy is closed when the helper exits.
	path := "/proc/self/fd/" + strconv.Itoa(3)
	command := exec.CommandContext(ctx, path)
	command.ExtraFiles = []*os.File{file}
	return command, cleanup, nil
}
