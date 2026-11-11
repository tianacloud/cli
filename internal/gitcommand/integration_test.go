//go:build linux || darwin

package gitcommand

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/testutil/compatpeer"
)

// Opt in with a locally built CLI plus its packaged helper. Git and pack/object
// transfer are real; the TLS/H2 server stands in for Gateway and the Git App.
func TestNativeGitPackRoundTrip(t *testing.T) {
	binary := os.Getenv("TIANA_TEST_CLI_BINARY")
	if binary == "" {
		t.Skip("set TIANA_TEST_CLI_BINARY to candidate CLI with adjacent helper")
	}
	for _, mode := range []string{"standard", "compat"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			git := func(dir string, args ...string) string {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.test", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.test")
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v %s", args, err, out)
				}
				return strings.TrimSpace(string(out))
			}
			bare := filepath.Join(root, "remote.git")
			git(root, "init", "--bare", "--initial-branch=main", bare)
			seed := filepath.Join(root, "seed")
			git(root, "init", "--initial-branch=main", seed)
			payload := bytes.Repeat([]byte("native-git\x00\xff"), 8192)
			if err := os.WriteFile(filepath.Join(seed, "payload"), payload, 0600); err != nil {
				t.Fatal(err)
			}
			git(seed, "add", "payload")
			git(seed, "commit", "-m", "fixture")
			git(seed, "push", bare, "main")
			host := "ep-01j5c9m7q2v8x4k6n3r0t1w2yz.example.test"
			key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			cert := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{host}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
			der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
			if err != nil {
				t.Fatal(err)
			}
			ca := filepath.Join(root, "ca.pem")
			if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
				t.Fatal(err)
			}
			var connections atomic.Int32
			var stall atomic.Bool
			entered, disconnected := make(chan struct{}, 1), make(chan struct{}, 1)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				connections.Add(1)
				if r.Method != "CONNECT" || r.Host != host+":443" || r.Header.Get("Tiana-Database-Protocol") != "git" || r.Header.Get("Proxy-Authorization") != "Bearer tia_0"+strings.Repeat("A", 43) {
					t.Error("Gateway request identity changed")
					w.WriteHeader(400)
					return
				}
				w.Header()["Date"] = nil
				w.Header()["Content-Type"] = nil
				w.Header().Set("Tiana-Tunnel-Version", "1")
				w.Header().Set("Tiana-Request-Id", r.Header.Get("Tiana-Request-Id"))
				w.Header().Set("Tiana-Auth-Mode", "TOKEN_REQUIRED")
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				if stall.Load() {
					entered <- struct{}{}
					select {
					case <-r.Context().Done():
						disconnected <- struct{}{}
					case <-time.After(5 * time.Second):
						t.Error("Git helper retained its Gateway stream after cancellation")
					}
					return
				}
				var length [4]byte
				if _, err := io.ReadFull(r.Body, length[:]); err != nil {
					t.Error(err)
					return
				}
				n, err := strconv.ParseUint(string(length[:]), 16, 16)
				if err != nil || n < 4 {
					t.Error("bad Git header")
					return
				}
				initial := make([]byte, n-4)
				if _, err := io.ReadFull(r.Body, initial); err != nil {
					t.Error(err)
					return
				}
				service, _, _ := strings.Cut(string(initial), " ")
				if service != "git-upload-pack" && service != "git-receive-pack" {
					t.Error("unexpected Git service")
					return
				}
				command := exec.CommandContext(r.Context(), "git", strings.TrimPrefix(service, "git-"), bare)
				command.Stdin = r.Body
				command.Stdout = flushWriter{w}
				command.Stderr = io.Discard
				command.WaitDelay = 100 * time.Millisecond
				_ = command.Run()
				r.Body.Close()
			}))
			if mode == "compat" {
				server.Listener = &compatpeer.Listener{Listener: server.Listener, Endpoint: host}
			}
			server.EnableHTTP2 = true
			server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS13}
			server.StartTLS()
			defer server.Close()
			for _, name := range []string{"ALL_PROXY", "all_proxy", "HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "TIANA_TOKEN_FILE"} {
				t.Setenv(name, "")
			}
			os.Unsetenv("TIANA_TOKEN_FILE")
			if mode == "compat" {
				t.Setenv("ALL_PROXY", "http://127.0.0.1:1")
			}
			t.Setenv("TIANA_GATEWAY_ADDRESS", server.Listener.Addr().String())
			t.Setenv("TIANA_TOKEN", "tia_0"+strings.Repeat("A", 43))
			run := func(dir string, args ...string) string {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				relativeCA, err := filepath.Rel(dir, ca)
				if err != nil {
					t.Fatal(err)
				}
				command := exec.CommandContext(ctx, binary, append([]string{"--transport", mode, "--ca-file", relativeCA, "git"}, args...)...)
				command.Dir = dir
				out, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("native %v: %v %s", args, err, out)
				}
				return string(out)
			}
			url := "tiana://" + host + "/repo.git"
			clone := filepath.Join(root, "clone with spaces")
			run(root, "clone", url, clone)
			got, err := os.ReadFile(filepath.Join(clone, "payload"))
			if err != nil || !bytes.Equal(got, payload) {
				t.Fatal("clone changed payload", err)
			}
			t.Setenv("GIT_ALLOW_PROTOCOL", "tiana")
			run(clone, "submodule", "add", url, "nested")
			run(clone, "fetch", "origin")
			run(clone, "pull", "--ff-only", "origin", "main")
			if err := os.WriteFile(filepath.Join(clone, "next"), []byte("second"), 0600); err != nil {
				t.Fatal(err)
			}
			git(clone, "add", "next")
			git(clone, "commit", "-m", "next")
			run(clone, "push", "origin", "main")
			want := git(clone, "rev-parse", "HEAD")
			if got := git(root, "--git-dir", bare, "rev-parse", "main"); got != want {
				t.Fatalf("push ref %s != %s", got, want)
			}
			if !strings.Contains(run(clone, "ls-remote", "origin", "refs/heads/main"), want) {
				t.Fatal("remote ref differs")
			}
			if git(clone, "remote", "get-url", "origin") != url || connections.Load() < 5 {
				t.Fatal("remote or connection coverage changed")
			}
			if out, err := exec.Command("git", "-C", clone, "config", "--local", "--get", "tiana.transport").Output(); err == nil || len(out) != 0 {
				t.Fatal("transport persisted")
			}
			stall.Store(true)
			command := exec.Command(binary, "--transport", mode, "--ca-file", ca, "git", "ls-remote", "origin")
			command.Dir = clone
			var diagnostics bytes.Buffer
			command.Stdout = &diagnostics
			command.Stderr = &diagnostics
			command.WaitDelay = 3 * time.Second
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				command.Process.Kill()
				<-done
				t.Fatal("native Git/helper did not reach Gateway")
			}
			if err := command.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
				if command.ProcessState.ExitCode() != 130 {
					t.Fatalf("Git cancellation: %d %s", command.ProcessState.ExitCode(), diagnostics.String())
				}
			case <-time.After(4 * time.Second):
				command.Process.Kill()
				<-done
				t.Fatal("native Git/helper did not exit")
			}
			select {
			case <-disconnected:
			case <-time.After(time.Second):
				t.Fatal("helper Gateway stream remained open")
			}
		})
	}
}

type flushWriter struct{ http.ResponseWriter }

func (w flushWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.ResponseWriter.(http.Flusher).Flush()
	return n, err
}
