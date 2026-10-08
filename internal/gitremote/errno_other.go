//go:build !windows

package gitremote

import "syscall"

const connectionRefused = syscall.ECONNREFUSED
