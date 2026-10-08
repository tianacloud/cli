//go:build darwin || linux

package supervisor

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

var bracketedPasteMarkers = [][]byte{[]byte("\x1b[200~"), []byte("\x1b[201~")}

// stripBracketedPaste removes the delimiters a terminal adds around pasted
// text when bracketed paste mode is enabled. The prompt never enables that
// mode itself, but a shell can leave it on for the foreground command, so a
// pasted Token would otherwise arrive wrapped and fail length validation.
func stripBracketedPaste(value []byte) []byte {
	for _, marker := range bracketedPasteMarkers {
		for {
			index := bytes.Index(value, marker)
			if index < 0 {
				break
			}
			value = append(value[:index], value[index+len(marker):]...)
		}
	}
	return value
}

func promptCredential(ctx context.Context) (*SecretToken, error) {
	terminal, err := os.OpenFile("/dev/tty", os.O_RDWR|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, missingCredentialError()
	}
	defer terminal.Close()
	return readTerminalCredential(ctx, terminal)
}

func readTerminalCredential(ctx context.Context, terminal *os.File) (token *SecretToken, resultErr error) {
	fd := int(terminal.Fd())
	// File.Fd may restore blocking mode; keep reads cancellable even when
	// terminal readiness changes between poll and read.
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		return nil, err
	}
	defer unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags)
	previous, err := unix.IoctlGetTermios(fd, tokenGetTermios)
	if err != nil {
		return nil, missingCredentialError()
	}
	state := *previous
	state.Lflag &^= unix.ECHO | unix.ECHONL
	state.Lflag |= unix.ICANON | unix.ISIG
	if err := unix.IoctlSetTermios(fd, tokenSetTermios, &state); err != nil {
		return nil, fmt.Errorf("disable Token input echo: %w", err)
	}
	defer func() {
		// Discard partially entered secrets on cancellation or invalid input.
		if resultErr != nil {
			flushTokenInput(fd)
		}
		if err := unix.IoctlSetTermios(fd, tokenSetTermios, previous); err != nil {
			if token != nil {
				token.Destroy()
				token = nil
			}
			resultErr = fmt.Errorf("restore terminal settings: %w", err)
		}
	}()
	if _, err := fmt.Fprint(terminal, "Enter instance connection Token (input hidden): "); err != nil {
		return nil, err
	}
	defer fmt.Fprintln(terminal)
	value := make([]byte, 0, maxTokenIn+1)
	defer func() { zeroBytes(value[:cap(value)]) }()
	var one [1]byte
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		events := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(events, 100)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read Token terminal: %w", err)
		}
		if n == 0 {
			continue
		}
		n, err = unix.Read(fd, one[:])
		if err == unix.EAGAIN || err == unix.EINTR {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read Token terminal: %w", err)
		}
		if n == 0 {
			return nil, fmt.Errorf("Token input cancelled")
		}
		if one[0] == '\n' {
			value = stripBracketedPaste(value)
			if len(value) == 0 {
				return nil, missingCredentialError()
			}
			return ParseToken(trimOneLineEnding(value))
		}
		value = append(value, one[0])
		if len(value) > maxTokenIn {
			return nil, fmt.Errorf("credential input is too large")
		}
	}
}
