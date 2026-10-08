//go:build windows

package gitremote

import "golang.org/x/sys/windows"

const connectionRefused = windows.WSAECONNREFUSED
