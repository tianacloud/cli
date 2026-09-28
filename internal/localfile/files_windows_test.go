//go:build windows

package localfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
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
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if err := PrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path, 64, true); err == nil {
		t.Fatal("accepted public ACL")
	}
	if data, err := Read(path, 64, false); err != nil || string(data) != "secret" {
		t.Fatalf("ordinary file: %q %v", data, err)
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

func TestWindowsPrivateDirectoryACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	if err := PrivateDir(path); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := sd.Owner()
	if err != nil || !owner.Equals(user.User.Sid) {
		t.Fatalf("directory owner: %v", err)
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount != 1 {
		t.Fatalf("directory must have one owner grant: %v", err)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(acl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != 0x001f01ff || !(*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(user.User.Sid) {
		t.Fatal("directory grants access outside current user")
	}
}
