//go:build windows

// Package consoleinput reads cancellable, bounded lines from a Windows console.
package consoleinput

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
)

var readConsoleInput = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReadConsoleInputW")

// INPUT_RECORD with its KEY_EVENT_RECORD union member.
type inputRecord struct {
	EventType       uint16
	_               uint16
	KeyDown         int32
	RepeatCount     uint16
	VirtualKeyCode  uint16
	VirtualScanCode uint16
	Char            uint16
	ControlKeyState uint32
}

// ReadLine restores console mode before returning. A nil echo hides input;
// otherwise characters and editing are echoed to the supplied console writer.
// The caller owns the returned bytes and should clear secret input after use.
func ReadLine(ctx context.Context, input *os.File, echo io.Writer, limit int) (value []byte, resultErr error) {
	handle := windows.Handle(input.Fd())
	var previous uint32
	if err := windows.GetConsoleMode(handle, &previous); err != nil {
		return nil, err
	}
	mode := (previous | windows.ENABLE_PROCESSED_INPUT | windows.ENABLE_EXTENDED_FLAGS) &^ (windows.ENABLE_ECHO_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_QUICK_EDIT_MODE | windows.ENABLE_VIRTUAL_TERMINAL_INPUT)
	if err := windows.SetConsoleMode(handle, mode); err != nil {
		return nil, err
	}
	value = make([]byte, 0, limit+utf8.UTFMax)
	defer func() {
		if resultErr != nil {
			_ = windows.FlushConsoleInputBuffer(handle)
		}
		if err := windows.SetConsoleMode(handle, previous); err != nil {
			resultErr = fmt.Errorf("restore terminal settings: %w", err)
		}
		if resultErr != nil {
			clear(value[:cap(value)])
			value = nil
		}
	}()
	write := func(text string) error {
		if echo == nil {
			return nil
		}
		_, err := io.WriteString(echo, text)
		return err
	}
	var high uint16
	for {
		if err := ctx.Err(); err != nil {
			return value, err
		}
		status, err := windows.WaitForSingleObject(handle, 100)
		if err != nil {
			return value, err
		}
		if status == uint32(windows.WAIT_TIMEOUT) {
			continue
		}
		var record inputRecord
		var count uint32
		ok, _, err := readConsoleInput.Call(uintptr(handle), uintptr(unsafe.Pointer(&record)), 1, uintptr(unsafe.Pointer(&count)))
		if ok == 0 {
			return value, fmt.Errorf("read console input: %w", err)
		}
		if count == 0 || record.EventType != windows.KEY_EVENT || record.KeyDown == 0 {
			continue
		}
		for repeat := uint16(0); repeat < record.RepeatCount; repeat++ {
			char := record.Char
			switch char {
			case 0:
				continue
			case '\r', '\n':
				return value, write("\r\n")
			case 3:
				return value, context.Canceled
			case 26:
				return value, io.EOF
			case '\b':
				high = 0
				if len(value) > 0 {
					_, size := utf8.DecodeLastRune(value)
					clear(value[len(value)-size:])
					value = value[:len(value)-size]
					if err := write("\b \b"); err != nil {
						return value, err
					}
				}
				continue
			}
			if char >= 0xD800 && char <= 0xDBFF {
				high = char
				continue
			}
			r := rune(char)
			if high != 0 {
				r = utf16.DecodeRune(rune(high), r)
				high = 0
			}
			value = utf8.AppendRune(value, r)
			if len(value) > limit {
				return value, errors.New("input exceeds limit")
			}
			if err := write(string(r)); err != nil {
				return value, err
			}
		}
	}
}
