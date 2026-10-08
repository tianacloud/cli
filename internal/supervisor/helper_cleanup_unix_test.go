//go:build linux || darwin

package supervisor

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestHelperCleanupWaitsForProcessExit(t *testing.T) {
	for _, stop := range []string{"exit", "close"} {
		t.Run(stop, func(t *testing.T) {
			cleaned := make(chan struct{})
			helper, err := launchHelperCommand(context.Background(),
				exec.Command("/bin/sh", "-c", "read line"), DefaultCredentialSource(),
				func() { close(cleaned) })
			if err != nil {
				t.Fatal(err)
			}
			defer helper.Close()
			select {
			case <-cleaned:
				t.Fatal("helper executable cleaned up before process exit")
			default:
			}
			process := helper.(*ProcessHelper)
			done := process.Done()
			if stop == "exit" {
				if _, err := process.write.Write([]byte("stop\n")); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("helper did not exit")
				}
			} else {
				if err := helper.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-cleaned:
			case <-time.After(5 * time.Second):
				t.Fatal("helper executable not cleaned up after process exit")
			}
		})
	}
}

func TestHelperStartFailureCleansUp(t *testing.T) {
	cleaned := false
	_, err := launchHelperCommand(context.Background(),
		exec.Command(t.TempDir()+"/missing-helper"), DefaultCredentialSource(),
		func() { cleaned = true })
	if err == nil || !cleaned {
		t.Fatalf("start failure: error=%v cleaned=%v", err, cleaned)
	}
}
