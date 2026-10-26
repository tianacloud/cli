package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/netconfig"
	"github.com/tianacloud/cli/internal/testutil/proxypeer"
	"github.com/tianacloud/cli/internal/updatecheck"
)

func TestUpdateWorkerProcessThroughAllProxy(t *testing.T) {
	binary := os.Getenv("TIANA_TEST_BUILTIN_CLI")
	if binary == "" {
		t.Skip("set TIANA_TEST_BUILTIN_CLI to a compiled CLI")
	}
	for _, kind := range []string{"http", "socks5h"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			// The worker deliberately has no registry override. Map its fixed
			// public registry through a local proxy to this synthetic TLS peer.
			const registry = "registry.npmjs.org"
			server, _ := proxypeer.TLS(t, registry, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Host != registry || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" {
					t.Error("worker registry identity or credential boundary")
				}
				io.WriteString(w, `{"version":"2.0.0"}`)
			}))
			var peer *proxypeer.Proxy
			if kind == "http" {
				peer = proxypeer.HTTP(t, server.Listener.Addr().String(), false)
			} else {
				peer = proxypeer.SOCKS(t, server.Listener.Addr().String(), false)
			}
			newTestEnv(t, "https://mgr.example.test")
			ca := filepath.Join(t.TempDir(), "ca.pem")
			if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			snapshot := netconfig.WithEnvironment(ctx, []string{"ALL_PROXY=" + peer.URL})
			cmd := exec.CommandContext(ctx, binary, updateWorkerArgument)
			cmd.Env = append(netconfig.HelperEnvironment(snapshot, os.Environ()), "TIANA_CA_FILE="+ca)
			var output, diagnostics bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &diagnostics
			if err := cmd.Run(); err != nil {
				t.Fatal("worker process", err)
			}
			if output.Len() != 0 || diagnostics.Len() != 0 || calls.Load() != 1 || len(peer.Requests()) != 1 {
				t.Fatalf("worker was noisy or bypassed proxy: calls=%d, accepts=%d", calls.Load(), len(peer.Requests()))
			}
			path, err := updatecheck.DefaultPath()
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var state updatecheck.State
			if err := json.Unmarshal(data, &state); err != nil || state.Packages[updatecheck.CLIPackage].LatestVersion != "2.0.0" || !state.LastNotifiedAt.IsZero() {
				t.Fatal("worker failed to save success or reserved a reminder", err)
			}
		})
	}
}
