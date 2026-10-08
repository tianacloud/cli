//go:build windows

package gitremote

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsGitFilePolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "中文 token")
	if err := writeFixtureFile(path, []byte("synthetic-token"), 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := readRegular(path, 15, true); err != nil || string(data) != "synthetic-token" {
		t.Fatalf("read Token: %q %v", data, err)
	}
	if _, err := readRegular(path, 3, true); err == nil {
		t.Fatal("accepted oversized token")
	}
	if err := chmodFixtureFile(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readRegular(path, 64, true); err == nil {
		t.Fatal("accepted public token")
	}
	if _, err := readRegular(path, 64, false); err != nil {
		t.Fatalf("public CA: %v", err)
	}
	link := path + ".link"
	if err := os.Symlink(path, link); err != nil {
		t.Skipf("symlinks need developer mode or privilege: %v", err)
	}
	if _, err := readRegular(link, 64, false); err != nil {
		t.Fatalf("CA symlink: %v", err)
	}
	if _, err := readRegular(link, 64, true); err == nil {
		t.Fatal("accepted token symlink")
	}
}
