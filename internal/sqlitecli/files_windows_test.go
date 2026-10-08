//go:build windows

package sqlitecli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsSQLFilesAndSymlinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "中文 script.sql")
	if err := os.WriteFile(path, []byte("SELECT 1;"), 0644); err != nil {
		t.Fatal(err)
	}
	if data, err := ReadFile(path, 9); err != nil || string(data) != "SELECT 1;" {
		t.Fatalf("read SQL: %q %v", data, err)
	}
	if _, err := ReadFile(path, 8); err == nil {
		t.Fatal("accepted oversized SQL")
	}
	for _, input := range []string{filepath.Dir(path), "NUL"} {
		if _, err := ReadFile(input, 64); err == nil {
			t.Fatal("accepted non-regular SQL input")
		}
	}
	link := path + ".link"
	if err := os.Symlink(path, link); err != nil {
		t.Skipf("symlinks need developer mode or privilege: %v", err)
	}
	if data, err := ReadFile(link, 9); err != nil || string(data) != "SELECT 1;" {
		t.Fatalf("SQL symlink: %q %v", data, err)
	}
}
