//go:build darwin || linux

package supervisor

import (
	"bytes"
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTerminalCredentialHiddenInputAndRestoration(t *testing.T) {
	for _, mode := range []string{"valid", "bracketed", "invalid", "empty", "eof", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			master, slave := openTestPTY(t)
			defer master.Close()
			defer slave.Close()
			before, err := unix.IoctlGetTermios(int(slave.Fd()), tokenGetTermios)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				token, err := readTerminalCredential(ctx, slave)
				if token != nil {
					token.Destroy()
				}
				done <- err
			}()
			prompted := make(chan error, 1)
			go func() {
				var output strings.Builder
				var buf [1]byte
				for !strings.HasSuffix(output.String(), ": ") {
					_, err := master.Read(buf[:])
					if err != nil {
						prompted <- err
						return
					}
					output.WriteByte(buf[0])
				}
				prompted <- nil
			}()
			select {
			case err := <-prompted:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("prompt timed out")
			}
			during, err := unix.IoctlGetTermios(int(slave.Fd()), tokenGetTermios)
			if err != nil {
				t.Fatal(err)
			}
			if during.Lflag&(unix.ECHO|unix.ECHONL) != 0 {
				t.Fatal("Token input would echo")
			}
			switch mode {
			case "valid":
				master.WriteString("tia_" + strings.Repeat("A", 43) + "\n")
			case "bracketed":
				master.WriteString("\x1b[200~tia_" + strings.Repeat("A", 43) + "\x1b[201~\n")
			case "invalid":
				master.WriteString("not-a-token\n")
			case "empty":
				master.WriteString("\n")
			case "eof":
				master.Write([]byte{4})
			case "cancel":
				cancel()
			}
			select {
			case err := <-done:
				maySucceed := mode == "valid" || mode == "bracketed"
				if maySucceed && err != nil {
					t.Fatal(err)
				}
				if !maySucceed && err == nil {
					t.Fatal("expected input error")
				}
				if mode == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("input did not finish")
			}
			after, err := unix.IoctlGetTermios(int(slave.Fd()), tokenGetTermios)
			if err != nil {
				t.Fatal(err)
			}
			if *before != *after {
				t.Fatal("terminal settings were not restored")
			}
		})
	}
}

func TestDefaultPromptRestoresTerminalOnInterrupt(t *testing.T) {
	const role = "TIANA_TEST_TOKEN_PROMPT_CHILD"
	if os.Getenv(role) == "1" {
		os.Unsetenv("TIANA_TOKEN")
		before, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), tokenGetTermios)
		if err != nil {
			t.Fatal(err)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		// The prompt uses /dev/tty even when the native input is a separate stream.
		input := strings.NewReader("SELECT 1;\n")
		t.Setenv("TIANA_INSTANCE_TOKENS_FILE", t.TempDir()+"/missing-tokens.json")
		endpoint, err := ParseEndpoint("ep-00000000000000000000000000.db.example.test")
		if err != nil {
			t.Fatal(err)
		}
		_, err = readConnectEndpointCredential(ctx, ConnectOptions{Credential: DefaultCredentialSource(), Interactive: true}, input, endpoint)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("interrupt result: %v", err)
		}
		after, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), tokenGetTermios)
		if err != nil {
			t.Fatal(err)
		}
		if *before != *after {
			t.Fatal("Ctrl-C did not restore terminal")
		}
		if input.Len() != len("SELECT 1;\n") {
			t.Fatal("native stdin consumed")
		}
		return
	}
	master, slave := openTestPTY(t)
	defer master.Close()
	defer slave.Close()
	command := exec.Command(os.Args[0], "-test.run=^TestDefaultPromptRestoresTerminalOnInterrupt$")
	command.Env = append(os.Environ(), role+"=1")
	command.Stdin = slave
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer command.Process.Kill()
	prompted := make(chan error, 1)
	go func() {
		var text strings.Builder
		var buf [1]byte
		for !strings.HasSuffix(text.String(), ": ") {
			_, err := master.Read(buf[:])
			if err != nil {
				prompted <- err
				return
			}
			text.WriteByte(buf[0])
		}
		prompted <- nil
	}()
	select {
	case err := <-prompted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("default prompt timed out")
	}
	go io.Copy(io.Discard, master)
	master.WriteString("partial-secret")
	master.Write([]byte{3})
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("child: %v %s", err, output.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Ctrl-C did not cancel prompt")
	}
}
