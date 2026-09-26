//go:build !windows

package apppublish

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestUploadCARejectsFIFOWithoutBlocking(t *testing.T) {
	file := filepath.Join(t.TempDir(), "ca.pem")
	if err := syscall.Mkfifo(file, 0600); err != nil {
		t.Skip(err)
	}
	done := make(chan *Error, 1)
	go func() { _, err := uploadClientWithCA(file); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("accepted FIFO CA")
		}
	case <-time.After(time.Second):
		// Unblock a buggy implementation before reporting the regression.
		f, err := os.OpenFile(file, os.O_RDWR|syscall.O_NONBLOCK, 0)
		if err == nil {
			f.Close()
		}
		t.Fatal("FIFO CA blocked instead of failing")
	}
}
