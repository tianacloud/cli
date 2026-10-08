package supervisor

import "golang.org/x/sys/unix"

const tokenGetTermios = unix.TCGETS
const tokenSetTermios = unix.TCSETS

func flushTokenInput(fd int) { _ = unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH) }
