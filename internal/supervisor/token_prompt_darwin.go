package supervisor

import "golang.org/x/sys/unix"

const tokenGetTermios = unix.TIOCGETA
const tokenSetTermios = unix.TIOCSETA

func flushTokenInput(fd int) { _ = unix.IoctlSetPointerInt(fd, unix.TIOCFLUSH, unix.TCIFLUSH) }
