//go:build windows

package supervisor

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestGitJobOwnsDescendants(t *testing.T) {
	const role = "TIANA_TEST_GIT_JOB_ROLE"
	switch os.Getenv(role) {
	case "descendant":
		time.Sleep(time.Minute)
		return
	case "parent":
		child := exec.Command(os.Args[0], "-test.run=^TestGitJobOwnsDescendants$")
		child.Env = append(os.Environ(), role+"=descendant")
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(os.Getenv("TIANA_TEST_GIT_JOB_PID"), []byte(strconv.Itoa(child.Process.Pid)), 0600); err != nil {
			t.Fatal(err)
		}
		_ = child.Wait()
		return
	}
	marker := filepath.Join(t.TempDir(), "child.pid")
	command := exec.Command(os.Args[0], "-test.run=^TestGitJobOwnsDescendants$")
	command.Env = append(os.Environ(), role+"=parent", "TIANA_TEST_GIT_JOB_PID="+marker)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() {
		code, err := RunGitProcess(ctx, command, IO{Stdout: io.Discard, Stderr: io.Discard})
		if err != nil {
			t.Error(err)
		}
		done <- code
	}()
	deadline := time.Now().Add(5 * time.Second)
	var process windows.Handle
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(marker)
		if err == nil {
			pid, err := strconv.Atoi(string(data))
			if err != nil {
				t.Fatal(err)
			}
			process, err = windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if process == 0 {
		cancel()
		<-done
		t.Fatal("Git descendant did not start")
	}
	defer windows.CloseHandle(process)
	cancel()
	select {
	case code := <-done:
		if code != 130 {
			t.Fatalf("cancellation exit code %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Git runner did not finish")
	}
	status, err := windows.WaitForSingleObject(process, 5000)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("descendant survived job cleanup: %d %v", status, err)
	}
}
