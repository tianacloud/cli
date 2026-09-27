//go:build linux || darwin

package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

func TestRunKeepsChildSIGPIPEDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		child := exec.Command("/bin/sh", "-c", "kill -PIPE $$; exit 97")
		err := child.Run()
		if err == nil || child.ProcessState == nil {
			t.Error("child ignored SIGPIPE")
		} else if status := child.ProcessState.Sys().(syscall.WaitStatus); !status.Signaled() || status.Signal() != syscall.SIGPIPE {
			t.Errorf("child status=%v", status)
		}
		if r.URL.Path == "/api/v1/usage" {
			fmt.Fprint(w, testQuotaResponse)
		} else {
			fmt.Fprint(w, `{"user":{"user_id":"usr_cli"}}`)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
	out, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	previous := os.Stdout
	os.Stdout = out
	defer func() { os.Stdout = previous }()
	if code := run([]string{"status"}); code != 0 {
		t.Fatalf("run exit=%d", code)
	}
}
