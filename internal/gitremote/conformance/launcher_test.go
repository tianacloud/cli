//go:build linux || darwin

package conformance

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLauncherUsesSiblingAndPreservesIO(t *testing.T) {
	wrapper, err := os.ReadFile("../../../scripts/git-remote-tiana")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "package with spaces")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write("git-remote-tiana", string(wrapper))
	write("tiana", "#!/bin/sh\nprintf '%s\\000' \"$@\"\ncat\nprintf diagnostic >&2\nexit 37\n")
	cmd := exec.Command(filepath.Join(dir, "git-remote-tiana"), "remote name", "tiana://endpoint/path with spaces", "")
	cmd.Stdin = bytes.NewBufferString("native input\x00\xff")
	var output, diagnostics bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &diagnostics
	err = cmd.Run()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 37 {
		t.Fatalf("exit status not preserved: %v", err)
	}
	want := "git\x00remote-helper\x00remote name\x00tiana://endpoint/path with spaces\x00\x00native input\x00\xff"
	if output.String() != want || diagnostics.String() != "diagnostic" {
		t.Fatalf("IO/argv changed: %q %q", output.String(), diagnostics.String())
	}
	// A PATH tiana must never stand in for a missing sibling.
	if err := os.Remove(filepath.Join(dir, "tiana")); err != nil {
		t.Fatal(err)
	}
	trapDir := t.TempDir()
	marker := filepath.Join(trapDir, "called")
	if err := os.WriteFile(filepath.Join(trapDir, "tiana"), []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(filepath.Join(dir, "git-remote-tiana"), "origin", "url")
	cmd.Env = append(os.Environ(), "PATH="+trapDir+":"+os.Getenv("PATH"))
	if err := cmd.Run(); err == nil {
		t.Fatal("missing sibling succeeded")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("launcher searched PATH for tiana")
	}
}
