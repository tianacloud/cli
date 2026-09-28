//go:build !windows

package supervisor

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeNativeFixture(t *testing.T, exitCode int) string {
	t.Helper()
	directory := t.TempDir()
	program := filepath.Join(directory, "turso")
	script := []byte("#!/bin/sh\nexit " + string(rune('0'+exitCode)) + "\n")
	if err := os.WriteFile(program, script, 0o755); err != nil {
		t.Fatal(err)
	}
	return program
}

func ownerExitTestCommand(t *testing.T) *exec.Cmd { t.Helper(); return exec.Command("sleep", "10") }
