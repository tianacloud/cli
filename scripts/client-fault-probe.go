//go:build ignore

// Run explicitly with go run. The peer never contacts a deployed service.
package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

func hash(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func must(err error) {
	if err != nil {
		panic(err)
	}
}

type event struct {
	ID                string `json:"request_id"`
	CredentialMatches bool   `json:"credential_matches"`
	Protocol          string `json:"protocol"`
}
type row struct {
	Client      string   `json:"client"`
	Credential  string   `json:"credential"`
	Fault       string   `json:"fault"`
	Diagnostics bool     `json:"diagnostics"`
	Exit        int      `json:"exit"`
	Requests    []event  `json:"requests"`
	PrintedIDs  []string `json:"printed_ids"`
	NativeBytes bool     `json:"native_bytes_received"`
	StdoutHash  string   `json:"stdout_sha256"`
	StderrHash  string   `json:"stderr_sha256"`
	Failures    []string `json:"failures"`
}

func main() { os.Exit(probe()) }

func probe() int {
	binary := flag.String("binary", "", "current candidate CLI")
	turso := flag.String("turso", "", "real Turso binary")
	output := flag.String("output", "", "JSON output")
	only := flag.String("only", "", "optional client name")
	onlyFault := flag.String("fault", "", "optional fault name")
	onlyCredential := flag.String("credential", "", "optional credential source")
	profile := flag.String("profile", "", "explicit connect profile")
	gitService := flag.String("git-service", "git-upload-pack", "native Git service")
	flag.Parse()
	if *binary == "" || *turso == "" || *output == "" {
		panic("binary, turso and output required")
	}
	dir, err := os.MkdirTemp("", "tiana-client-fault-")
	must(err)
	defer os.RemoveAll(dir)
	host := "ep-00000000000000000000000000.db.example.test"
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(err)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	must(err)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(err)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{host}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
	must(err)
	caPath := filepath.Join(dir, "ca.pem")
	must(os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0600))
	base := []string{}
	for _, value := range os.Environ() {
		name := strings.SplitN(value, "=", 2)[0]
		if !strings.HasPrefix(name, "TIANA_") && !strings.HasPrefix(name, "TURSO_") && !strings.HasPrefix(name, "LIBSQL_") && !strings.Contains(strings.ToLower(name), "proxy") {
			base = append(base, value)
		}
	}
	rows := []row{}
	ids := regexp.MustCompile(`req-[A-Za-z0-9_-]+`)
	for _, client := range []string{"sqlite", "git", "connect"} {
		if *only != "" && *only != client {
			continue
		}
		for _, credential := range []string{"account", "env", "file", "both", "empty", "public-file", "symlink", "fifo", "missing-file", "oversize", "whitespace", "newline", "expired-account", "missing-account"} {
			if *onlyCredential != "" && *onlyCredential != credential {
				continue
			}
			faults := []string{"forbidden"}
			if credential == "env" {
				faults = append(faults, "bad-envelope", "reset", "cancel-before-200", "cancel-after-native")
				if client == "sqlite" {
					faults = append(faults, "timeout-before-200", "timeout-after-native")
				}
				if client == "git" {
					faults = append(faults, "stdout-pipe")
				}
			}
			for _, diagnostic := range []bool{false, true} {
				for _, fault := range faults {
					if *onlyFault != "" && *onlyFault != fault {
						continue
					}
					root, err := os.MkdirTemp(dir, "case-")
					must(err)
					accountPath := filepath.Join(root, "account.json")
					expiry := time.Now().Add(time.Hour)
					if credential == "expired-account" {
						expiry = time.Now().Add(-time.Hour)
					}
					must(authclient.NewFileStore(accountPath, "https://mgr.example.test").Save(authclient.Credential{AccessToken: "fixture-account", RefreshToken: "fixture-refresh", ExpiresAt: expiry}))
					if credential == "missing-account" {
						must(os.Remove(accountPath))
					}
					tokenPath := filepath.Join(root, "token")
					must(os.WriteFile(tokenPath, []byte("fixture-file\r\n"), 0600))
					env := append(append([]string{}, base...), "TIANA_CA_FILE="+caPath, "TIANA_API_ORIGIN=https://mgr.example.test", "TIANA_CREDENTIALS_FILE="+accountPath)
					if diagnostic {
						env = append(env, "TIANA_DIAGNOSTICS=1")
					}
					expected := "fixture-account"
					switch credential {
					case "env":
						expected = "fixture-env"
						env = append(env, "TIANA_TOKEN="+expected)
					case "file":
						expected = "fixture-file"
						env = append(env, "TIANA_TOKEN_FILE="+tokenPath)
					case "both":
						env = append(env, "TIANA_TOKEN=fixture-env", "TIANA_TOKEN_FILE="+tokenPath)
					case "empty":
						env = append(env, "TIANA_TOKEN=")
					case "public-file":
						must(os.Chmod(tokenPath, 0644))
						env = append(env, "TIANA_TOKEN_FILE="+tokenPath)
					case "symlink":
						must(os.Symlink(tokenPath, tokenPath+".link"))
						env = append(env, "TIANA_TOKEN_FILE="+tokenPath+".link")
					case "fifo":
						must(syscall.Mkfifo(tokenPath+".fifo", 0600))
						env = append(env, "TIANA_TOKEN_FILE="+tokenPath+".fifo")
					case "missing-file":
						env = append(env, "TIANA_TOKEN_FILE="+tokenPath+".missing")
					case "oversize":
						env = append(env, "TIANA_TOKEN="+strings.Repeat("x", 513))
					case "whitespace":
						env = append(env, "TIANA_TOKEN= fixture-env")
					case "newline":
						env = append(env, "TIANA_TOKEN=fixture-env\nsecond")
					}
					var lock sync.Mutex
					observed := []event{}
					native := false
					reached := make(chan struct{}, 1)
					pipeClosed := make(chan struct{})
					server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						lock.Lock()
						observed = append(observed, event{r.Header.Get("Tiana-Request-Id"), r.Header.Get("Proxy-Authorization") == "Bearer "+expected, r.Header.Get("Tiana-Database-Protocol")})
						lock.Unlock()
						if fault == "cancel-before-200" || fault == "timeout-before-200" {
							reached <- struct{}{}
							<-r.Context().Done()
							return
						}
						w.Header()["Date"] = nil
						w.Header()["Content-Type"] = nil
						if fault == "forbidden" {
							w.Header().Set("Tiana-Error-Code", "ACCESS_DENIED")
							w.WriteHeader(403)
							return
						}
						w.Header().Set("Tiana-Tunnel-Version", "1")
						w.Header().Set("Tiana-Request-Id", r.Header.Get("Tiana-Request-Id"))
						w.Header().Set("Tiana-Auth-Mode", "TOKEN_REQUIRED")
						if fault == "bad-envelope" {
							w.Header().Set("Tiana-Request-Id", "req-wrong")
						}
						w.WriteHeader(200)
						w.(http.Flusher).Flush()
						if fault == "bad-envelope" {
							return
						}
						buffer := make([]byte, 1)
						n, _ := r.Body.Read(buffer)
						lock.Lock()
						native = n > 0
						lock.Unlock()
						if fault == "reset" {
							panic(http.ErrAbortHandler)
						}
						if fault == "stdout-pipe" {
							reached <- struct{}{}
							<-pipeClosed
							_, _ = w.Write(bytes.Repeat([]byte("pack-fixture"), 8192))
							w.(http.Flusher).Flush()
							return
						}
						reached <- struct{}{}
						<-r.Context().Done()
					}))
					server.EnableHTTP2 = true
					server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
					server.StartTLS()
					env = append(env, "TIANA_GATEWAY_ADDRESS="+server.Listener.Addr().String())
					argv := []string{"sqlite", "shell", "--endpoint", host + ":" + strings.Split(server.Listener.Addr().String(), ":")[1], "-e", "SELECT 1;"}
					if strings.HasPrefix(fault, "timeout-") {
						argv = append(argv, "--timeout", "200")
					}
					input := ""
					if client == "git" {
						argv = []string{"git", "remote-helper", "origin", "tiana://" + host + "/repo.git"}
						input = "capabilities\nconnect " + *gitService + "\n0033" + *gitService + " /repo.git\x00host=fixture\x00"
					}
					if client == "connect" {
						argv = []string{"connect", "--allow-unisolated-loopback", "--", "turso", "db", "shell", "https://" + host + ":" + strings.Split(server.Listener.Addr().String(), ":")[1], "SELECT 1;"}
						if *profile != "" {
							argv = append([]string{"connect", "--profile", *profile}, argv[1:]...)
						}
					}
					ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
					cmd := exec.CommandContext(ctx, *binary, argv...)
					if client == "connect" {
						env = append(env, "PATH="+filepath.Dir(*turso)+string(os.PathListSeparator)+os.Getenv("PATH"))
					}
					cmd.Env = env
					cmd.Stdin = strings.NewReader(input)
					cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
					cmd.WaitDelay = time.Second
					var stdout, stderr bytes.Buffer
					cmd.Stdout = &stdout
					cmd.Stderr = &stderr
					var pipeReader, pipeWriter *os.File
					if fault == "stdout-pipe" {
						pipeReader, pipeWriter, err = os.Pipe()
						must(err)
						cmd.Stdout = pipeWriter
					}
					must(cmd.Start())
					if pipeWriter != nil {
						must(pipeWriter.Close())
					}
					done := make(chan error, 1)
					go func() { done <- cmd.Wait() }()
					if fault == "stdout-pipe" {
						select {
						case <-reached:
						case <-ctx.Done():
						case e := <-done:
							done <- e
						}
						must(pipeReader.Close())
						close(pipeClosed)
					}
					if strings.HasPrefix(fault, "cancel-") {
						select {
						case <-reached:
							must(syscall.Kill(-cmd.Process.Pid, syscall.SIGINT))
						case <-ctx.Done():
						case e := <-done:
							done <- e
						}
					}
					<-done
					cancel()
					_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
					server.CloseClientConnections()
					server.Close()
					lock.Lock()
					rec := row{Client: client, Credential: credential, Fault: fault, Diagnostics: diagnostic, Exit: cmd.ProcessState.ExitCode(), Requests: observed, NativeBytes: native, PrintedIDs: ids.FindAllString(stderr.String(), -1), StdoutHash: hash(stdout.Bytes()), StderrHash: hash(stderr.Bytes()), Failures: []string{}}
					lock.Unlock()
					valid := credential == "env" || credential == "file" || credential == "account"
					if rec.Exit == 0 {
						rec.Failures = append(rec.Failures, "fault returned success")
					}
					if valid && len(rec.Requests) != 1 {
						rec.Failures = append(rec.Failures, fmt.Sprintf("want one CONNECT got %d", len(rec.Requests)))
					}
					if !valid && (len(rec.Requests) != 0 || len(rec.PrintedIDs) != 0) {
						rec.Failures = append(rec.Failures, "invalid local credential reached request lifecycle")
					}
					for _, e := range rec.Requests {
						if !e.CredentialMatches || e.ID == "" || !strings.Contains(stderr.String(), e.ID) {
							rec.Failures = append(rec.Failures, "wire credential/ID diagnostic mismatch")
						}
					}
					if (fault == "reset" || fault == "cancel-after-native" || fault == "timeout-after-native") && !rec.NativeBytes {
						rec.Failures = append(rec.Failures, "native bytes not reached")
					}
					if strings.HasPrefix(fault, "timeout-") && rec.Exit == 130 {
						rec.Failures = append(rec.Failures, "deadline classified as user cancellation")
					}
					if strings.HasPrefix(fault, "cancel-") && rec.Exit != 130 {
						rec.Failures = append(rec.Failures, "cancel exit not130")
					}
					for _, secret := range []string{"fixture-account", "fixture-env", "fixture-file", "fixture-refresh"} {
						if strings.Contains(stdout.String()+stderr.String(), secret) {
							rec.Failures = append(rec.Failures, "credential exposed")
						}
					}
					rows = append(rows, rec)
					data, _ := json.MarshalIndent(rows, "", "  ")
					must(os.WriteFile(*output, data, 0600))
					must(os.RemoveAll(root))
					if len(rec.Failures) > 0 {
						fmt.Printf("FAIL %s %s %s diag=%v %v\n", client, credential, fault, diagnostic, rec.Failures)
						fmt.Println(strings.NewReplacer("fixture-account", "[fixture]", "fixture-env", "[fixture]", "fixture-file", "[fixture]", "fixture-refresh", "[fixture]").Replace(stderr.String()))
					}
				}
			}
		}
	}
	b, err := os.ReadFile(*binary)
	must(err)
	failures := 0
	for _, r := range rows {
		if len(r.Failures) > 0 {
			failures++
		}
	}
	data, _ := json.MarshalIndent(map[string]any{"binary_sha256": hash(b), "rows": rows, "passed": len(rows) - failures, "failed": failures, "fixture_cleaned": true}, "", "  ")
	must(os.WriteFile(*output, data, 0600))
	fmt.Printf("passed=%d failed=%d\n", len(rows)-failures, failures)
	if failures > 0 {
		return 1
	}
	return 0
}
