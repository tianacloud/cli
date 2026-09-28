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
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == BuiltinHelperArgument {
		if ServeBuiltinHelper(context.Background(), os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if filepath.Base(os.Args[0]) == "turso.exe" {
		data, err := os.ReadFile(filepath.Join(filepath.Dir(os.Args[0]), "exit-code"))
		if err != nil {
			os.Exit(98)
		}
		status, err := strconv.Atoi(string(data))
		if err != nil {
			os.Exit(99)
		}
		os.Exit(status)
	}
	os.Exit(m.Run())
}

func writeNativeFixture(t *testing.T, status int) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "原生 client")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "turso.exe")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(target, source)
	closeErr := target.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("copy native fixture: %v %v", err, closeErr)
	}
	if err := os.WriteFile(filepath.Join(directory, "exit-code"), []byte(strconv.Itoa(status)), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func ownerExitTestCommand(t *testing.T) *exec.Cmd {
	t.Helper()
	child, _ := windowsNativeChild(t, "wait")
	return child
}

func TestWindowsBuiltinHelperReexec(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	helper, err := (BuiltinHelperLauncher{}).Launch(ctx, DefaultCredentialSource())
	if err != nil {
		t.Fatal(err)
	}
	defer helper.Close()
	if _, err := helper.Handshake(ctx, []uint16{3}, "windows-reexec"); err != nil {
		t.Fatal(err)
	}
	if err := helper.Configure(ctx, builtinTestConfig()); err != nil {
		t.Fatal(err)
	}
	if err := helper.DeliverCredential(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := helper.WaitBound(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := helper.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	identity, err := newChildIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.ChildStarted(ctx, identity); err != nil {
		t.Fatal(err)
	}
	if err := helper.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if err := helper.WaitStopped(ctx); err != nil {
		t.Fatal(err)
	}
}
