//go:build linux

package supervisor

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	realTursoVersion = "v1.0.32"
	// This is the independently frozen SHA-256 of the extracted v1.0.32
	// executable. The archive digest is provenance-only; the binary itself is
	// an injected test input and is never checked into the repo.
	realTursoBinarySHA256 = "257ef10bf753477f0fc6cd0754e346aade78bd3614fa8e71fb7eb8b3df4ef31f"
)

func TestRealTursoV1032PositionalLocatorCaptures(t *testing.T) {
	path := os.Getenv("TIANA_TURSO_BIN")
	if path == "" {
		t.Skip("set TIANA_TURSO_BIN to the injected Turso v1.0.32 binary")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read injected Turso binary: %v", err)
	}
	digest := sha256.Sum256(contents)
	if actual := hex.EncodeToString(digest[:]); actual != realTursoBinarySHA256 {
		t.Fatalf("Turso binary digest=%s, want frozen v1.0.32 binary digest %s", actual, realTursoBinarySHA256)
	}
	versionOutput, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("Turso --version: %v (%s)", err, strings.TrimSpace(string(versionOutput)))
	}
	if !strings.Contains(string(versionOutput), realTursoVersion) {
		t.Fatalf("Turso version=%q, want %s", strings.TrimSpace(string(versionOutput)), realTursoVersion)
	}

	for _, test := range []struct {
		profile string
		scheme  string
		method  string
		path    string
	}{
		{profile: ProfileHranaHTTP, scheme: "http", method: "POST", path: "/v2/pipeline"},
		{profile: ProfileHranaWebSocket, scheme: "ws", method: "GET", path: "/"},
	} {
		t.Run(test.profile, func(t *testing.T) {
			raw := runTursoLocatorCapture(t, path, test.profile, test.scheme)
			requestLine, headers := splitHTTPHeaders(t, raw)
			wantLine := fmt.Sprintf("%s %s HTTP/1.1", test.method, test.path)
			if requestLine != wantLine {
				t.Fatalf("first request line=%q, want %q; raw=%q", requestLine, wantLine, raw)
			}
			if !strings.Contains(headers, "Host: 127.0.0.1:") && !strings.Contains(headers, "host: 127.0.0.1:") {
				t.Fatalf("request did not target the loopback positional locator: %q", headers)
			}
		})
	}
}

func runTursoLocatorCapture(t *testing.T, path, profile, scheme string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	type captureResult struct {
		request string
		err     error
	}
	result := make(chan captureResult, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			result <- captureResult{err: fmt.Errorf("accept Turso fixture connection: %w", acceptErr)}
			return
		}
		defer connection.Close()
		_ = connection.SetReadDeadline(time.Now().Add(4 * time.Second))
		reader := bufio.NewReader(connection)
		var captured bytes.Buffer
		for captured.Len() < 64*1024 {
			value, readErr := reader.ReadString('\n')
			captured.WriteString(value)
			if strings.Contains(captured.String(), "\r\n\r\n") || readErr != nil {
				break
			}
		}
		// The first line is sufficient to prove the positional locator was
		// authoritative; return a bounded error response and close.
		_, _ = io.WriteString(connection, "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		if captured.Len() == 0 {
			result <- captureResult{err: fmt.Errorf("Turso closed the fixture connection before sending a request")}
			return
		}
		result <- captureResult{request: captured.String()}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	locator := scheme + "://127.0.0.1:" + strconv.Itoa(port)
	prepared, err := (SQLDAdapter{}).Prepare(
		LocalEndpoint{
			Network:       "tcp",
			Address:       "127.0.0.1:" + strconv.Itoa(port),
			URL:           locator,
			SecurityLevel: SecurityLoopbackUnisolated,
		},
		profile,
		[]string{path, "db", "shell", testEndpointURL(t), "SELECT 1"},
		[]string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + os.Getenv("HOME"),
			"LANG=C",
			// A conflicting route value proves that the adapter removes the
			// remote route before installing its authoritative local locator.
			"TURSO_DATABASE_URL=https://invalid.invalid:1",
		},
	)
	if err != nil {
		t.Fatalf("prepare real Turso argv: %v", err)
	}
	if prepared.Argv[3] != locator || prepared.Argv[4] != "SELECT 1" || strings.Contains(strings.Join(prepared.Argv, "\x00"), ".db.example.test") {
		t.Fatalf("prepared argv=%q", prepared.Argv)
	}
	command := exec.CommandContext(ctx, prepared.Program, prepared.Argv[1:]...)
	command.Env = prepared.Env
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err == nil {
		t.Logf("Turso returned success after the bounded fixture response")
	} else if ctx.Err() != nil {
		t.Fatalf("Turso capture timed out: %v; stderr=%q", ctx.Err(), stderr.String())
	}
	select {
	case capture := <-result:
		if capture.err != nil {
			t.Fatal(capture.err)
		}
		return capture.request
	case <-time.After(time.Second):
		t.Fatal("Turso request capture did not complete")
	}
	return ""
}

func splitHTTPHeaders(t *testing.T, raw string) (string, string) {
	t.Helper()
	line, headers, ok := strings.Cut(raw, "\r\n")
	if !ok {
		t.Fatalf("malformed captured request: %q", raw)
	}
	return line, headers
}
