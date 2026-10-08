//go:build windows

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestWindowsDeleteConfirmation(t *testing.T) {
	if os.Getenv("TIANA_WINDOWS_CONFIRMATION_TEST") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWindowsDeleteConfirmation$")
		command.Env = append(os.Environ(), "TIANA_WINDOWS_CONFIRMATION_TEST=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("console confirmation: %v\n%s", err, output)
		}
		return
	}
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	kernel.NewProc("FreeConsole").Call()
	if ok, _, err := kernel.NewProc("AllocConsole").Call(); ok == 0 {
		t.Fatal(err)
	}
	defer kernel.NewProc("FreeConsole").Call()
	input, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	handle := windows.Handle(input.Fd())
	var before uint32
	if err := windows.GetConsoleMode(handle, &before); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, input        string
		want, cancel, fail bool
	}{
		{name: "yes", input: "yes\r", want: true},
		{name: "short", input: "Y\r", want: true},
		{name: "whitespace", input: " YES \r", want: true},
		{name: "edit", input: "yex\bs\r", want: true},
		{name: "no", input: "no\r"},
		{name: "empty", input: "\r"},
		{name: "other", input: "yes please\r"},
		{name: "eof", input: "\x1a"},
		{name: "incomplete", input: "yes", cancel: true},
		{name: "long", input: strings.Repeat("y", 65) + "\r", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			windows.FlushConsoleInputBuffer(handle)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				// Give the synchronous reader time to prepare its console mode.
				time.Sleep(50 * time.Millisecond)
				for _, r := range tc.input {
					var record [20]byte
					binary.LittleEndian.PutUint16(record[0:], windows.KEY_EVENT)
					binary.LittleEndian.PutUint32(record[4:], 1)
					binary.LittleEndian.PutUint16(record[8:], 1)
					binary.LittleEndian.PutUint16(record[14:], uint16(r))
					var written uint32
					ok, _, err := kernel.NewProc("WriteConsoleInputW").Call(uintptr(handle), uintptr(unsafe.Pointer(&record)), 1, uintptr(unsafe.Pointer(&written)))
					if ok == 0 {
						result <- err
						cancel()
						return
					}
				}
				if tc.cancel {
					time.Sleep(50 * time.Millisecond)
					cancel()
				}
				result <- nil
			}()
			confirmed, err := readDeleteConfirmation(ctx, input)
			if confirmed != tc.want {
				t.Fatalf("confirmed=%v want=%v", confirmed, tc.want)
			}
			if tc.cancel {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel=%v", err)
				}
			} else if (err != nil) != tc.fail {
				t.Fatalf("error=%v", err)
			}
			if err := <-result; err != nil {
				t.Fatal(err)
			}
			var after uint32
			if err := windows.GetConsoleMode(handle, &after); err != nil || before != after {
				t.Fatalf("console mode %x -> %x: %v", before, after, err)
			}
		})
	}
}
