//go:build linux || darwin

package conformance

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

type flushWriter struct{ http.ResponseWriter }

func (w flushWriter) Write(p []byte) (int, error) {
	n, e := w.ResponseWriter.Write(p)
	w.ResponseWriter.(http.Flusher).Flush()
	return n, e
}

// The fixture exercises real TLS 1.3/H2 regular CONNECT and real Git. It is
// explicitly not a deployed Gateway/Control/Agent acceptance test.
func TestNativeGitOverConnect(t *testing.T) {
	binary := os.Getenv("TEST_GIT_REMOTE")
	if binary == "" {
		t.Skip("build Go package and set TEST_GIT_REMOTE and TEST_TIANA_CLI")
	}
	dir := filepath.Clean(t.TempDir())
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(bin, "git-remote-tiana"), data, 0700); err != nil {
		t.Fatal(err)
	}
	cli, err := os.ReadFile(os.Getenv("TEST_TIANA_CLI"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(bin, "tiana"), cli, 0700); err != nil {
		t.Fatal(err)
	}

	hostname := "ep-00000000000000000000000000.git.example.test"
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caCert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Git conformance CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caCert, caCert, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{hostname}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0600); err != nil {
		t.Fatal(err)
	}
	var calls, early atomic.Int64
	var denied, reset, stall atomic.Bool
	started := make(chan struct{}, 1)
	var expectedToken atomic.Value
	expectedToken.Store("Bearer account-secret")
	repository := filepath.Join(dir, "repo.git")
	run := func(env []string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Env = env
		cmd.WaitDelay = 2 * time.Second
		return cmd.CombinedOutput()
	}
	baseEnv := []string{}
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "TIANA_") && !strings.HasPrefix(v, "GIT_") {
			baseEnv = append(baseEnv, v)
		}
	}
	baseEnv = append(baseEnv, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	out, err := run(baseEnv, "init", "--bare", "--initial-branch=main", repository)
	if err != nil {
		t.Fatalf("init: %s %v", out, err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header()["Date"] = nil
		w.Header()["Content-Type"] = nil
		if r.Method != "CONNECT" || r.URL.Path != "" || r.URL.Scheme != "" || r.ProtoMajor != 2 || r.Host != hostname+":443" || r.TLS.Version != tls.VersionTLS13 || r.Header.Get("Tiana-Database-Protocol") != "git" || r.Header.Get("Tiana-Tunnel-Version") != "1" || r.Header.Get("Tiana-Request-Id") == "" {
			t.Errorf("invalid CONNECT envelope")
			w.WriteHeader(400)
			return
		}
		if r.Header.Get("Proxy-Authorization") != expectedToken.Load().(string) {
			t.Errorf("wrong credential header")
			w.WriteHeader(407)
			return
		}
		if stall.Load() {
			started <- struct{}{}
			<-r.Context().Done()
			return
		}
		if denied.Load() {
			w.Header().Set("Tiana-Error-Code", "ACCESS_DENIED")
			w.WriteHeader(403)
			return
		}
		first := make(chan []byte, 1)
		go func() { b := make([]byte, 1); n, _ := r.Body.Read(b); first <- b[:n] }()
		var prefix []byte
		select {
		case prefix = <-first:
			early.Add(1)
		case <-time.After(20 * time.Millisecond):
		}
		w.Header().Set("Tiana-Tunnel-Version", "1")
		w.Header().Set("Tiana-Request-Id", r.Header.Get("Tiana-Request-Id"))
		w.Header().Set("Tiana-Auth-Mode", "DISABLED")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		if reset.Load() {
			panic(http.ErrAbortHandler)
		}
		if prefix == nil {
			prefix = <-first
		}
		cmd := exec.CommandContext(r.Context(), "git", "daemon", "--inetd", "--log-destination=stderr", "--export-all", "--strict-paths", "--enable=receive-pack", "--base-path="+dir, repository)
		cmd.Env = baseEnv
		cmd.Stdin = io.MultiReader(bytes.NewReader(prefix), r.Body)
		cmd.Stdout = flushWriter{w}
		var daemonLog bytes.Buffer
		cmd.Stderr = &daemonLog
		if err := cmd.Run(); err != nil {
			t.Logf("fixture daemon: %v %s", err, daemonLog.String())
		}
	}))
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	server.StartTLS()
	defer server.Close()
	localStore := filepath.Join(dir, "instance-tokens.json")
	accountPath := filepath.Join(dir, "credentials.json")
	if err := authclient.NewFileStore(accountPath, "https://mgr.example.test").Save(authclient.Credential{AccessToken: "account-secret", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	env := append(append([]string{}, baseEnv...), "TIANA_MGR_ORIGIN=https://mgr.example.test", "TIANA_CREDENTIALS_FILE="+accountPath, "TIANA_INSTANCE_TOKENS_FILE="+localStore, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "TIANA_CA_FILE="+ca, "TIANA_GATEWAY_ADDRESS="+server.Listener.Addr().String(), "GIT_TERMINAL_PROMPT=0")
	url := "tiana://" + hostname + ":" + strings.Split(server.Listener.Addr().String(), ":")[1] + "/repo.git"
	must := func(args ...string) string {
		t.Helper()
		out, e := run(env, args...)
		if e != nil {
			t.Fatalf("git %v: %v %s", args, e, out)
		}
		return strings.TrimSpace(string(out))
	}
	client := filepath.Join(dir, "client")
	must("init", "--initial-branch=main", client)
	must("-C", client, "config", "user.name", "Conformance")
	must("-C", client, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(client, "file"), []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	// Incompressible content exceeds both copy buffers and H2 flow-control windows.
	blob := make([]byte, 1024*1024)
	if _, err := rand.Read(blob); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(client, "blob"), blob, 0600); err != nil {
		t.Fatal(err)
	}
	must("-C", client, "add", ".")
	must("-C", client, "commit", "-m", "first")
	first := must("-C", client, "rev-parse", "HEAD")
	must("-C", client, "remote", "add", "origin", url)
	must("-C", client, "push", "origin", "main")
	clone := filepath.Join(dir, "clone")
	must("clone", url, clone)
	content, _ := os.ReadFile(filepath.Join(clone, "file"))
	if string(content) != "first" {
		t.Fatal("clone content mismatch")
	}
	received, err := os.ReadFile(filepath.Join(clone, "blob"))
	if err != nil || !bytes.Equal(received, blob) {
		t.Fatal("bulk pack content mismatch")
	}
	if err := os.WriteFile(filepath.Join(client, "file"), []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}
	must("-C", client, "commit", "-am", "second")
	second := must("-C", client, "rev-parse", "HEAD")
	must("-C", client, "push", "origin", "main")
	must("-C", client, "reset", "--hard", first)
	if _, e := run(env, "-C", client, "push", "origin", "main"); e == nil {
		t.Fatal("ordinary non-fast-forward accepted")
	}
	must("-C", client, "push", "--force", "origin", "main")
	if _, e := run(env, "-C", client, "push", "--force-with-lease=main:"+second, "origin", second+":refs/heads/main"); e == nil {
		t.Fatal("stale lease accepted")
	}
	must("-C", client, "push", "--force-with-lease=main:"+first, "origin", second+":refs/heads/main")
	must("-C", clone, "fetch", "origin")
	if got := must("-C", clone, "rev-parse", "origin/main"); got != second {
		t.Fatal("fetch mismatch")
	}
	must("clone", "--depth=1", url, filepath.Join(dir, "shallow"))
	must("ls-remote", "tiana::https://"+strings.TrimPrefix(url, "tiana://"))
	denied.Store(true)
	before := calls.Load()
	out, err = run(env, "ls-remote", url)
	if err == nil || calls.Load() != before+1 {
		t.Fatalf("denial must fail without retry: %v %s", err, out)
	}
	denied.Store(false)
	// A saved Token must reach the real CONNECT header without MGR or a
	// credential prompt. Explicit env/file sources below override this Token.
	savedToken := "tia_" + strings.Repeat("B", 42) + "A"
	if err = authclient.NewFileStore(accountPath, "https://mgr.example.test").Save(authclient.Credential{AccessToken: savedToken, RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(localStore, []byte("broken legacy cache"), 0600); err != nil {
		t.Fatal(err)
	}
	beforeStore, err := os.ReadFile(localStore)
	if err != nil {
		t.Fatal(err)
	}
	expectedToken.Store("Bearer " + savedToken)
	out, err = run(env, "ls-remote", url)
	if err != nil || bytes.Contains(out, []byte(savedToken)) {
		t.Fatalf("saved Token: %v %s", err, out)
	}
	afterStore, err := os.ReadFile(localStore)
	if err != nil || !bytes.Equal(beforeStore, afterStore) {
		t.Fatal("helper mutated local Token store", err)
	}
	token := "tia_" + strings.Repeat("A", 43)
	expectedToken.Store("Bearer " + token)
	tokenEnv := append(append([]string{}, env...), "TIANA_TOKEN="+token)
	out, err = run(tokenEnv, "ls-remote", url)
	if err != nil {
		t.Fatalf("token: %v %s", err, out)
	}
	if bytes.Contains(out, []byte(token)) {
		t.Fatal("token leaked")
	}
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte(token+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fileEnv := append(append([]string{}, env...), "TIANA_TOKEN_FILE="+tokenFile)
	if out, err := run(fileEnv, "ls-remote", url); err != nil {
		t.Fatalf("private token file: %v %s", err, out)
	}
	rejectBeforeConnect := func(testEnv []string) {
		t.Helper()
		before := calls.Load()
		out, err := run(testEnv, "ls-remote", url)
		if err == nil || calls.Load() != before || bytes.Contains(out, []byte(token)) {
			t.Fatalf("configuration must reject before CONNECT without token leak: %v %s", err, out)
		}
	}
	rejectBeforeConnect(append(append([]string{}, fileEnv...), "TIANA_TOKEN="+token))
	if err := os.Chmod(tokenFile, 0644); err != nil {
		t.Fatal(err)
	}
	rejectBeforeConnect(fileEnv)
	if err := os.Chmod(tokenFile, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "token-link")
	if err := os.Symlink(tokenFile, link); err != nil {
		t.Fatal(err)
	}
	rejectBeforeConnect(append(append([]string{}, env...), "TIANA_TOKEN_FILE="+link))
	fifo := filepath.Join(dir, "token-fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	rejectBeforeConnect(append(append([]string{}, env...), "TIANA_TOKEN_FILE="+fifo))
	rejectBeforeConnect(append(append([]string{}, env...), "TIANA_TOKEN="))
	if e := authclient.NewFileStore(accountPath, "https://mgr.example.test").Save(authclient.Credential{AccessToken: "account-secret", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}); e != nil {
		t.Fatal(e)
	}
	if err := os.Remove(localStore); err != nil {
		t.Fatal(err)
	}
	expectedToken.Store("Bearer account-secret")
	reset.Store(true)
	before = calls.Load()
	out, err = run(env, "ls-remote", url)
	if err == nil || calls.Load() != before+1 {
		t.Fatalf("post-200 reset must fail without replay: %v %s", err, out)
	}
	reset.Store(false)
	stall.Store(true)
	helper := exec.Command(filepath.Join(bin, "git-remote-tiana"), "origin", url)
	helper.Env = env
	input, err := helper.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var output, errors bytes.Buffer
	helper.Stdout, helper.Stderr = &output, &errors
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	defer helper.Process.Kill()
	defer input.Close()
	if _, err := io.WriteString(input, "capabilities\nconnect git-upload-pack\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("helper did not reach CONNECT")
	}
	if err := helper.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- helper.Wait() }()
	select {
	case err := <-exited:
		if err == nil {
			t.Fatal("canceled helper succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper cancellation hung with stdin open")
	}
	if output.String() != "connect\n\n" {
		t.Fatalf("canceled CONNECT emitted native/success bytes: %q", output.String())
	}
	stall.Store(false)

	expectedToken.Store("Bearer account-secret")
	withoutCA := []string{}
	for _, v := range env {
		if !strings.HasPrefix(v, "TIANA_CA_FILE=") {
			withoutCA = append(withoutCA, v)
		}
	}
	before = calls.Load()
	out, err = run(withoutCA, "ls-remote", url)
	if err == nil || calls.Load() != before {
		t.Fatalf("untrusted TLS must fail before CONNECT: %v %s", err, out)
	}
	if early.Load() != 0 {
		t.Fatal("native DATA sent before CONNECT 200")
	}
	t.Logf("native push/clone/fetch/force/lease/shallow, token/file rejection, cancellation, reset/denied CONNECT/no retry, TLS and pre-200 checks passed (%d CONNECT requests)", calls.Load())
}
