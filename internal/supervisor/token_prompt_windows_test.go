//go:build windows

package supervisor

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsCredentialConsole(t *testing.T) {
	if os.Getenv("TIANA_WINDOWS_CONSOLE_TEST") != "1" {
		child := exec.Command(os.Args[0], "-test.run=^TestWindowsCredentialConsole$")
		child.Env = append(os.Environ(), "TIANA_WINDOWS_CONSOLE_TEST=1")
		if output, err := child.CombinedOutput(); err != nil {
			t.Fatalf("console child: %v\n%s", err, output)
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
	output, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	handle := windows.Handle(input.Fd())
	var before uint32
	if err := windows.GetConsoleMode(handle, &before); err != nil {
		t.Fatal(err)
	}
	if !isTerminalFD(input.Fd()) {
		t.Fatal("console not reported as terminal")
	}
	for _, tc := range []struct {
		name, value, want string
		cancel            bool
	}{
		{name: "input", value: "abc\bD\r", want: "abD"},
		{name: "cancel", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			windows.FlushConsoleInputBuffer(handle)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				deadline := time.Now().Add(time.Second)
				for {
					var mode uint32
					if err := windows.GetConsoleMode(handle, &mode); err != nil {
						result <- err
						return
					}
					if mode&windows.ENABLE_ECHO_INPUT == 0 {
						break
					}
					if time.Now().After(deadline) {
						result <- errors.New("echo remained enabled")
						cancel()
						return
					}
					time.Sleep(time.Millisecond)
				}
				if tc.cancel {
					cancel()
					result <- nil
					return
				}
				for _, r := range tc.value {
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
				result <- nil
			}()
			token, err := readWindowsTerminalCredential(ctx, input, output)
			if tc.cancel {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel error: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer token.Destroy()
				if string(token.BytesForHandoff()) != tc.want {
					t.Fatal("incorrect token")
				}
			}
			if err := <-result; err != nil {
				t.Fatal(err)
			}
			var after uint32
			if err := windows.GetConsoleMode(handle, &after); err != nil || before != after {
				t.Fatalf("console settings changed: %x -> %x, %v", before, after, err)
			}
		})
	}
}
