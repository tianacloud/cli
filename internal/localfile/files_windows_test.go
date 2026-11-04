//go:build windows

package localfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsPrivateFilesAndLimits(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "中文 directory")
	if err := PrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	f, err := CreateTemp(dir, "credential-*")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	if _, err := f.Write([]byte("secret")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if data, err := Read(path, 6, true); err != nil || string(data) != "secret" {
		t.Fatalf("private read: %q %v", data, err)
	}
	if _, err := Read(path, 5, true); err == nil {
		t.Fatal("accepted oversized file")
	}
	if _, err := Read(dir, 64, false); err == nil {
		t.Fatal("accepted directory")
	}
	if _, err := Read("NUL", 64, false); err == nil {
		t.Fatal("accepted device")
	}
	if _, err := Read(filepath.Join(dir, "missing"), 64, true); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}

}

func TestWindowsPrivateReadRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	if err := PrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	f, err := CreateTemp(dir, "target-*")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	link := filepath.Join(dir, "link")
	if err := os.Symlink(f.Name(), link); err != nil {
		t.Skipf("symlink creation needs developer mode or privilege: %v", err)
	}
	if _, err := Read(link, 64, true); err == nil {
		t.Fatal("accepted symlink")
	}
}
