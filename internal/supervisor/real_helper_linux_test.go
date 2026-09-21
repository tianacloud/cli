//go:build linux

package supervisor

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const realNativeChildMarker = "__tiana_real_native_child"

// TestMain turns the package's already-built test binary into a tiny native
// client fixture when it is invoked through the basename "turso". The
// fixture deliberately has no shell, script, or alternate helper protocol.
// The fixture control data is carried in one SQL positional argument so the
// production adapter still sees the same single-SQL grammar as a real Turso
// invocation.
func TestMain(m *testing.M) {
	for _, argument := range os.Args {
		if recordPath, releasePath, ok := decodeRealNativeChildPayload(argument); ok {
			os.Exit(runRealNativeChild([]string{recordPath, releasePath}))
		}
	}
	os.Exit(m.Run())
}

func decodeRealNativeChildPayload(argument string) (string, string, bool) {
	prefix := "SELECT " + realNativeChildMarker + "\x1f"
	if !strings.HasPrefix(argument, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(argument, prefix), "\x1f")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func runRealNativeChild(arguments []string) int {
	if len(arguments) != 2 {
		return 2
	}
	recordPath, releasePath := arguments[0], arguments[1]
	var record strings.Builder
	record.WriteString("MODE=")
	record.WriteString(realNativeChildMarker)
	record.WriteByte('\n')
	for _, argument := range os.Args {
		record.WriteString("ARG=")
		record.WriteString(argument)
		record.WriteByte('\n')
	}
	for _, entry := range os.Environ() {
		record.WriteString("ENV=")
		record.WriteString(entry)
		record.WriteByte('\n')
	}
	if entries, err := os.ReadDir("/proc/self/fd"); err == nil {
		for _, entry := range entries {
			target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
			if err != nil {
				continue
			}
			record.WriteString("FD=")
			record.WriteString(entry.Name())
			record.WriteByte('=')
			record.WriteString(target)
			record.WriteByte('\n')
		}
	}
	if err := os.WriteFile(recordPath, []byte(record.String()), 0o600); err != nil {
		return 3
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		if _, err := os.Stat(releasePath); err == nil {
			return 0
		}
		if time.Now().After(deadline) {
			return 4
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type realHelperObservation struct {
	mu          sync.Mutex
	helper      *ProcessHelper
	command     *exec.Cmd
	reader      *bufio.Reader
	rawStdout   *os.File
	controlFDs  []int
	ready       chan struct{}
	readyClosed bool
}

// realHelperLauncher retains only test observations around the unchanged
// production ProcessHelperLauncher. It never substitutes a fake helper.
type realHelperLauncher struct {
	inner HelperLauncher
	obs   *realHelperObservation
}

func (l *realHelperLauncher) Launch(ctx context.Context, source CredentialSource) (HelperClient, error) {
	helperClient, err := l.inner.Launch(ctx, source)
	if err != nil {
		return nil, err
	}
	helper, ok := helperClient.(*ProcessHelper)
	if !ok {
		return nil, fmt.Errorf("production launcher returned %T, want *ProcessHelper", helperClient)
	}
	command := helper.command
	observation := l.obs
	observation.mu.Lock()
	observation.helper = helper
	observation.command = command
	observation.reader = helper.reader
	for _, value := range []any{helper.write, helper.read} {
		file, ok := value.(*os.File)
		if ok {
			flags, flagsErr := getFDFlags(int(file.Fd()))
			if flagsErr != nil {
				observation.controlFDs = append(observation.controlFDs, -1)
				continue
			}
			observation.controlFDs = append(observation.controlFDs, flags)
		}
	}
	if len(observation.controlFDs) != 2 {
		observation.mu.Unlock()
		_ = helper.Close()
		return nil, fmt.Errorf("production launcher exposed %d control pipe files, want 2", len(observation.controlFDs))
	}
	if file, ok := helper.read.(*os.File); ok {
		fd, dupErr := syscall.Dup(int(file.Fd()))
		if dupErr != nil {
			observation.mu.Unlock()
			_ = helper.Close()
			return nil, fmt.Errorf("duplicate helper stdout observer: %w", dupErr)
		}
		syscall.CloseOnExec(fd)
		observation.rawStdout = os.NewFile(uintptr(fd), "real-helper-stdout-observer")
	} else {
		observation.mu.Unlock()
		_ = helper.Close()
		return nil, fmt.Errorf("production launcher returned non-file helper stdout")
	}
	if !observation.readyClosed {
		close(observation.ready)
		observation.readyClosed = true
	}
	observation.mu.Unlock()
	return &realOwnerExitHelper{ProcessHelper: helper}, nil
}

// realOwnerExitHelper is a test-only wrapper around the real ProcessHelper.
// Supervisor.Run normally asks Drain after the native child exits. The
// owner-exit blackbox must prove that the compiled helper has already emitted
// STOPPED because its pidfd owner disappeared, without sending DRAIN first.
type realOwnerExitHelper struct {
	*ProcessHelper
	stopped bool
}

func (h *realOwnerExitHelper) Drain(ctx context.Context) error {
	if h.stopped {
		return nil
	}
	kind, payload, err := h.readFrameContext(ctx)
	if err != nil || kind != kindStopped || len(payload) != 0 {
		return protocolResponseError(kind, err)
	}
	h.stopped = true
	h.mu.Lock()
	h.state = StateStopped
	h.mu.Unlock()
	return waitForProcessGone(ctx, h.command.Process.Pid)
}

func (h *realOwnerExitHelper) WaitStopped(context.Context) error {
	if !h.stopped {
		return ErrHelperProtocol
	}
	return nil
}

func TestRealCompiledHelperThroughSupervisor(t *testing.T) {
	helperSource := os.Getenv("TIANA_REAL_HELPER")
	if helperSource == "" {
		t.Skip("set TIANA_REAL_HELPER to a Rust 1.85.1 tiana-helper binary")
	}
	if os.Geteuid() != 0 {
		t.Skip("system-helper blackbox requires a root-managed trusted install")
	}

	helperBytes, err := os.ReadFile(helperSource)
	if err != nil {
		t.Fatalf("read TIANA_REAL_HELPER: %v", err)
	}
	nativeBytes, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatalf("read Go test binary: %v", err)
	}
	digest := sha256.Sum256(helperBytes)
	root := createRealHelperInstall(t, helperBytes, digest, nativeBytes)
	trusted, err := ResolveTrustedHelper(root)
	if err != nil {
		t.Fatalf("ResolveTrustedHelper: %v", err)
	}

	nativeDir := filepath.Join(root, "native-bin")
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", nativeDir+string(os.PathListSeparator)+oldPath)
	const tokenEnv = "TIANA_REAL_BLACKBOX_TOKEN"
	const token = "tia_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	t.Setenv(tokenEnv, token)
	t.Setenv("TIANA_BLACKBOX_SECRET", token)

	for _, test := range []struct {
		name    string
		profile string
		scheme  string
	}{
		{name: "default-classifier", scheme: "http"},
		{name: "explicit-hrana-http", profile: ProfileHranaHTTP, scheme: "http"},
		{name: "explicit-hrana-websocket", profile: ProfileHranaWebSocket, scheme: "ws"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runRealCompiledHelperCase(
				t,
				ProcessHelperLauncher{Helper: trusted},
				trusted.Path(),
				0,
				test.profile,
				test.scheme,
			)
		})
	}
}

func TestRealEmbeddedCompiledHelperThroughSupervisor(t *testing.T) {
	helperSource := os.Getenv("TIANA_REAL_HELPER")
	if helperSource == "" {
		t.Skip("set TIANA_REAL_HELPER to a Rust 1.85.1 tiana-helper binary")
	}

	helperBytes, err := os.ReadFile(helperSource)
	if err != nil {
		t.Fatalf("read TIANA_REAL_HELPER: %v", err)
	}
	digest := sha256.Sum256(helperBytes)
	launcher, err := NewEmbeddedHelperLauncher(helperBytes, hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatalf("NewEmbeddedHelperLauncher: %v", err)
	}
	nativeBytes, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatalf("read Go test binary: %v", err)
	}
	nativeDir := t.TempDir()
	nativePath := filepath.Join(nativeDir, "turso")
	if err = os.WriteFile(nativePath, nativeBytes, 0o555); err != nil {
		t.Fatalf("write native fixture: %v", err)
	}
	t.Setenv("PATH", nativeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	const tokenEnv = "TIANA_REAL_BLACKBOX_TOKEN"
	const token = "tia_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	t.Setenv(tokenEnv, token)
	t.Setenv("TIANA_BLACKBOX_SECRET", token)

	for _, test := range []struct {
		name    string
		profile string
		scheme  string
	}{
		{name: "default-classifier", scheme: "http"},
		{name: "explicit-hrana-http", profile: ProfileHranaHTTP, scheme: "http"},
		{name: "explicit-hrana-websocket", profile: ProfileHranaWebSocket, scheme: "ws"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runRealCompiledHelperCase(
				t,
				launcher,
				"/proc/self/fd/3",
				1,
				test.profile,
				test.scheme,
			)
		})
	}
}

func runRealCompiledHelperCase(
	t *testing.T,
	launcher HelperLauncher,
	expectedCommandPath string,
	expectedExtraFiles int,
	profile string,
	expectedScheme string,
) {
	t.Helper()

	recordPath := filepath.Join(t.TempDir(), "native-record.txt")
	releasePath := filepath.Join(t.TempDir(), "release")
	nativePayload := "SELECT " + realNativeChildMarker + "\x1f" + recordPath + "\x1f" + releasePath
	const tokenEnv = "TIANA_REAL_BLACKBOX_TOKEN"
	const token = "tia_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

	observation := &realHelperObservation{ready: make(chan struct{})}
	observedLauncher := &realHelperLauncher{inner: launcher, obs: observation}
	var nativeStdout, nativeStderr bytes.Buffer
	supervisor := NewSupervisor(observedLauncher)
	supervisor.IO = IO{Stdin: strings.NewReader(""), Stdout: &nativeStdout, Stderr: &nativeStderr}
	supervisor.DrainTimeout = 3 * time.Second
	supervisor.StopTimeout = 2 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	result := make(chan struct {
		status int
		err    error
	}, 1)
	endpoint := testEndpoint(t)
	go func() {
		status, runErr := supervisor.Run(ctx, ConnectOptions{
			NativeArgv: []string{"turso", "db", "shell", "https://" + endpoint.Hostname(), nativePayload},
			AdapterID:  SQLDAdapterID,
			Profile:    profile,
			Credential: CredentialSource{Kind: CredentialFromEnvironment, Value: tokenEnv},
			Security:   SecurityPolicy{AllowUnisolated: true},
		})
		result <- struct {
			status int
			err    error
		}{status: status, err: runErr}
	}()

	select {
	case <-observation.ready:
	case <-ctx.Done():
		t.Fatalf("production helper was not launched: %v", ctx.Err())
	}

	var helper *ProcessHelper
	deadline := time.Now().Add(5 * time.Second)
	for {
		observation.mu.Lock()
		helper = observation.helper
		observation.mu.Unlock()
		if helper != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("production helper observation was not published")
		}
		time.Sleep(5 * time.Millisecond)
	}
	observation.mu.Lock()
	controlFDFlags := append([]int(nil), observation.controlFDs...)
	observation.mu.Unlock()
	for _, flags := range controlFDFlags {
		if flags < 0 || flags&syscall.FD_CLOEXEC == 0 {
			t.Fatalf("control fd was not recorded with FD_CLOEXEC: flags=%d", flags)
		}
	}

	deadline = time.Now().Add(8 * time.Second)
	for {
		helper.mu.Lock()
		state := helper.state
		helper.mu.Unlock()
		if state == StateServing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("helper never reached SERVING; state=%s", state)
		}
		time.Sleep(5 * time.Millisecond)
	}
	servingHelperFDs := procFDTargets(helper.command.Process.Pid)
	if err := os.WriteFile(releasePath, []byte("release"), 0o600); err != nil {
		t.Fatalf("release native child: %v", err)
	}

	select {
	case outcome := <-result:
		if outcome.err != nil || outcome.status != 0 {
			t.Fatalf("Supervisor.Run status=%d err=%v", outcome.status, outcome.err)
		}
	case <-ctx.Done():
		t.Fatalf("Supervisor.Run timeout: %v", ctx.Err())
	}

	observation.mu.Lock()
	command := observation.command
	reader := observation.reader
	rawStdout := observation.rawStdout
	observation.mu.Unlock()
	if command == nil || command.ProcessState == nil || command.ProcessState.ExitCode() != 0 {
		t.Fatalf("helper process did not exit 0: command=%v state=%v", command != nil, processExitCode(command))
	}
	if len(command.Args) != 1 || command.Args[0] != expectedCommandPath {
		t.Fatalf("helper received unexpected argv: %#v", command.Args)
	}
	if len(command.ExtraFiles) != expectedExtraFiles {
		t.Fatalf("helper received ExtraFiles=%d, want %d", len(command.ExtraFiles), expectedExtraFiles)
	}
	if command.Stdin == nil || command.Stdout == nil || command.Stderr != io.Discard {
		t.Fatalf("helper control streams were not private pipes/discard stderr: stdin=%T stdout=%T stderr=%T", command.Stdin, command.Stdout, command.Stderr)
	}
	if reader == nil || reader.Buffered() != 0 {
		t.Fatalf("helper stdout retained %d buffered tail bytes", bufferedBytes(reader))
	}
	if rawStdout != nil {
		defer rawStdout.Close()
		tail := readWithDeadline(t, rawStdout)
		if len(tail) != 0 {
			t.Fatalf("helper stdout had bytes after STOPPED: %x", tail)
		}
	}
	hasPipe, hasSocket := false, false
	for target := range servingHelperFDs {
		hasPipe = hasPipe || strings.HasPrefix(target, "pipe:[")
		hasSocket = hasSocket || strings.HasPrefix(target, "socket:[")
	}
	if !hasPipe || !hasSocket {
		t.Fatalf("SERVING helper fd snapshot lacks control pipe/socket: %#v", servingHelperFDs)
	}
	if strings.Contains(nativeStdout.String(), token) || strings.Contains(nativeStderr.String(), token) {
		t.Fatalf("native stdout/stderr contained credential canary: stdout=%q stderr=%q", nativeStdout.String(), nativeStderr.String())
	}

	recordBytes, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read native child record: %v", err)
	}
	record := string(recordBytes)
	if strings.Contains(record, token) {
		t.Fatal("credential canary reached native argv/env/fd record")
	}
	if !strings.Contains(record, "MODE="+realNativeChildMarker+"\n") {
		t.Fatalf("native child mode marker was not recorded:\n%s", record)
	}
	if !strings.Contains(record, "ARG="+nativePayload+"\n") {
		t.Fatalf("native child did not receive the single SQL fixture payload:\n%s", record)
	}
	if !strings.Contains(record, "ENV=TURSO_DATABASE_URL="+expectedScheme+"://127.0.0.1:") {
		t.Fatalf("native child did not receive the %s helper loopback locator:\n%s", expectedScheme, record)
	}
	for _, forbidden := range []string{"TIANA_REAL_BLACKBOX_TOKEN=", "TIANA_BLACKBOX_SECRET=", "TURSO_AUTH_TOKEN=", "LIBSQL_AUTH_TOKEN=", "--url", "--host", "--port", "--socket", "--proxy"} {
		if strings.Contains(record, forbidden) {
			t.Fatalf("native child record contains forbidden %q:\n%s", forbidden, record)
		}
	}
	for target := range servingHelperFDs {
		if target != "" && strings.Contains(record, "="+target+"\n") {
			t.Fatalf("native child inherited helper fd target %q:\n%s", target, record)
		}
	}
}

func getFDFlags(fd int) (int, error) {
	value, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), uintptr(syscall.F_GETFD), 0)
	if errno != 0 {
		return 0, errno
	}
	return int(value), nil
}

func waitForProcessGone(ctx context.Context, pid int) error {
	if pid <= 0 {
		return ErrHelperProtocol
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func createRealHelperInstall(t *testing.T, helperBytes []byte, digest [sha256.Size]byte, nativeBytes []byte) string {
	t.Helper()
	root, err := os.MkdirTemp("/usr/local", ".tiana-real-helper-")
	if err != nil {
		t.Fatalf("create trusted install root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	libexec := filepath.Join(root, "libexec")
	tianaDir := filepath.Join(libexec, "tiana")
	nativeDir := filepath.Join(root, "native-bin")
	if err := os.MkdirAll(tianaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(nativeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	helperPath := filepath.Join(tianaDir, "tiana-helper")
	if err := os.WriteFile(helperPath, helperBytes, 0o555); err != nil {
		t.Fatalf("write trusted helper: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nativeDir, "turso"), nativeBytes, 0o555); err != nil {
		t.Fatalf("write native fixture: %v", err)
	}
	manifestBytes, err := json.Marshal(helperManifest{
		ContractVersion:    HelperContractVersion,
		HelperRelativePath: trustedHelperRelativePath,
		SHA256:             hex.EncodeToString(digest[:]),
		Platform:           runtime.GOOS,
		Arch:               runtime.GOARCH,
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(tianaDir, trustedManifestName)
	if err := os.WriteFile(manifestPath, manifestBytes, 0o444); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, libexec, tianaDir, nativeDir} {
		if err := os.Chmod(path, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(helperPath, 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(manifestPath, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(nativeDir, "turso"), 0o555); err != nil {
		t.Fatal(err)
	}
	return root
}

func procFDTargets(pid int) map[string]struct{} {
	result := make(map[string]struct{})
	entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		return result
	}
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join("/proc", fmt.Sprint(pid), "fd", entry.Name()))
		if err == nil {
			result[target] = struct{}{}
		}
	}
	return result
}

func assertOrderedArguments(t *testing.T, record string, expected []string) {
	t.Helper()
	arguments := make([]string, 0, len(expected))
	for _, line := range strings.Split(record, "\n") {
		if strings.HasPrefix(line, "ARG=") {
			arguments = append(arguments, strings.TrimPrefix(line, "ARG="))
		}
	}
	for index, value := range expected {
		found := -1
		for candidate := index; candidate < len(arguments); candidate++ {
			if arguments[candidate] == value {
				found = candidate
				break
			}
		}
		if found < 0 {
			t.Fatalf("native argv missing ordered argument %q after index %d: %#v", value, index, arguments)
		}
		arguments = arguments[found:]
	}
}

func readWithDeadline(t *testing.T, reader io.Reader) []byte {
	t.Helper()
	result := make(chan []byte, 1)
	go func() {
		value, err := io.ReadAll(reader)
		if err != nil {
			result <- []byte(fmt.Sprintf("read error: %v", err))
			return
		}
		result <- value
	}()
	select {
	case value := <-result:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for helper stdout EOF")
		return nil
	}
}

func bufferedBytes(reader *bufio.Reader) int {
	if reader == nil {
		return -1
	}
	return reader.Buffered()
}

func processExitCode(command *exec.Cmd) any {
	if command == nil || command.ProcessState == nil {
		return "unknown"
	}
	return command.ProcessState.ExitCode()
}
