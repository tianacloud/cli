//go:build linux || darwin

package supervisor

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestGitProcessExitAndCancellation(t *testing.T) {
	var output bytes.Buffer
	code, err := RunGitProcess(context.Background(), exec.Command("sh", "-c", "printf native; exit 17"), IO{Stdout: &output, Stderr: &output})
	if err != nil || code != 17 || output.String() != "native" {
		t.Fatalf("%d %v %q", code, err, output.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	// The descendant retains stdout and ignores SIGINT. Wait can finish only
	// when cancellation terminates the group, including that descendant.
	code, err = RunGitProcess(ctx, exec.Command("sh", "-c", "trap '' INT; sleep 30 & wait"), IO{Stdout: &output, Stderr: &output})
	if err != nil || code != 130 || time.Since(start) > 3*time.Second {
		t.Fatalf("cancel: %d %v %s", code, err, time.Since(start))
	}
}
