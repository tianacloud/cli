//go:build windows

package supervisor

import (
	"bufio"
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"reflect"
	"testing"
	"time"
)

func TestWindowsNativeChild(t *testing.T) {
	if os.Getenv("TIANA_WINDOWS_NATIVE_CHILD") != "1" {
		return
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	switch os.Getenv("TIANA_WINDOWS_NATIVE_MODE") {
	case "interrupt":
		os.Stdout.Write([]byte("ready\n"))
		<-signals
		os.Exit(23)
	case "exit":
		os.Exit(17)
	default:
		io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
}

func windowsNativeChild(t *testing.T, mode string) (*exec.Cmd, io.WriteCloser) {
	t.Helper()
	child := exec.Command(os.Args[0], "-test.run=^TestWindowsNativeChild$")
	child.Env = append(os.Environ(), "TIANA_WINDOWS_NATIVE_CHILD=1", "TIANA_WINDOWS_NATIVE_MODE="+mode)
	configureNativeProcess(child)
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		input.Close()
		if child.Process != nil {
			child.Process.Kill()
		}
	})
	return child, input
}

func TestWindowsOwnerExitAndCancellation(t *testing.T) {
	for _, cancellation := range []bool{false, true} {
		t.Run(map[bool]string{false: "exit", true: "cancel"}[cancellation], func(t *testing.T) {
			child, input := windowsNativeChild(t, "wait")
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			defer child.Wait()
			identity, err := newChildIdentity(child.Process.Pid)
			if err != nil || identity.StartTime == 0 {
				t.Fatalf("identity=%+v err=%v", identity, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wrong := identity
			wrong.StartTime++
			if _, err := watchBuiltinOwner(ctx, wrong); !errors.Is(err, ErrHelperProtocol) {
				t.Fatalf("wrong creation time: %v", err)
			}
			done, err := watchBuiltinOwner(ctx, identity)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
				t.Fatal("live child observed as exited")
			default:
			}
			if cancellation {
				cancel()
			} else {
				input.Close()
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("owner watcher did not finish")
			}
			input.Close()
		})
	}
}

func TestWindowsNativeExitStatus(t *testing.T) {
	child, _ := windowsNativeChild(t, "exit")
	err := child.Run()
	if code := nativeExitCode(child, err); code != 17 {
		t.Fatalf("exit code=%d err=%v", code, err)
	}
}

func TestWindowsTerminalDetection(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	if isTerminalFD(read.Fd()) || isTerminalFD(write.Fd()) {
		t.Fatal("pipe reported as terminal")
	}
	file, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if isTerminalFD(file.Fd()) {
		t.Fatal("regular file reported as terminal")
	}
}

func TestWindowsNativeAdapterAndEnvironment(t *testing.T) {
	registry := NewAdapterRegistry()
	for _, program := range []string{"turso.exe", "TURSO.EXE", `C:\Program Files\客户端\turso.exe`} {
		explicit := ""
		if len(program) > 10 {
			explicit = SQLDAdapterID
		}
		adapter, err := registry.Select(program, explicit)
		if err != nil {
			t.Fatalf("%s: %v", program, err)
		}
		if _, err := adapter.ResolveEndpoint([]string{program, "db", "shell", "https://ep-00000000000000000000000000.db.example.test"}); err != nil {
			t.Fatal(err)
		}
	}
	got := helperEnvironment([]string{"Path=C:\\Tools", "PATH=duplicate", "SystemRoot=C:\\Windows", "USERPROFILE=C:\\Users\\test", "APPDATA=C:\\Users\\test\\AppData\\Roaming", "tiana_token=secret", "https_proxy=http://proxy", "CustomSecret=secret"}, CredentialSource{Kind: CredentialFromEnvironment, Value: "CUSTOMSECRET"})
	want := []string{"Path=C:\\Tools", "SystemRoot=C:\\Windows", "USERPROFILE=C:\\Users\\test", "APPDATA=C:\\Users\\test\\AppData\\Roaming"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment=%q", got)
	}
}

func TestWindowsNativeInterrupt(t *testing.T) {
	if os.Getenv("TIANA_WINDOWS_INTERRUPT_TEST") != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestWindowsNativeInterrupt$")
		command.Env = append(os.Environ(), "TIANA_WINDOWS_INTERRUPT_TEST=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("interrupt child: %v\n%s", err, output)
		}
		return
	}
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	kernel.NewProc("FreeConsole").Call()
	if ok, _, err := kernel.NewProc("AllocConsole").Call(); ok == 0 {
		t.Fatal(err)
	}
	defer kernel.NewProc("FreeConsole").Call()
	child, _ := windowsNativeChild(t, "interrupt")
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(output).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- child.Wait() }()
	forwardNativeSignal(child, os.Interrupt)
	select {
	case err := <-wait:
		if code := nativeExitCode(child, err); code != 23 {
			t.Fatalf("interrupt handler exit=%d err=%v", code, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("native process did not receive interrupt")
	}
}
