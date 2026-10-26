package supervisor

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tianacloud/cli/internal/netconfig"
	"github.com/tianacloud/cli/internal/testutil/proxypeer"
)

func TestExternalHelpersRejectProxyBeforeCredentialOrLaunch(t *testing.T) {
	for _, launcher := range []HelperLauncher{ProcessHelperLauncher{}, &ProcessHelperLauncher{}, &EmbeddedHelperLauncher{}} {
		for _, bypass := range []string{"", "*"} {
			ctx := netconfig.WithEnvironment(context.Background(), []string{"ALL_PROXY=http://user:proxy-canary@proxy.example.test", "NO_PROXY=" + bypass})
			s := NewSupervisor(launcher)
			s.IO = IO{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard}
			code, err := s.Run(ctx, ConnectOptions{NativeArgv: []string{"turso", "db", "shell", testEndpointURL(t)}, Credential: CredentialSource{Kind: CredentialFromEnvironment, Value: "TIANA_MISSING_TEST_CREDENTIAL"}})
			if code != 1 || err == nil {
				t.Fatalf("external helper accepted unsupported route: code=%d err=%v", code, err)
			}
			unsupported := strings.Contains(err.Error(), "does not support proxy routing")
			if unsupported != (bypass == "") || errors.Is(err, ErrHelperNotTrusted) || strings.Contains(err.Error(), "proxy-canary") {
				t.Fatalf("wrong external helper admission (%T, bypass=%q): %v", launcher, bypass, err)
			}
		}
	}
}

func TestBuiltinHelperProxyRouteAndNativeEnvironment(t *testing.T) {
	for _, kind := range []string{"http", "socks5h"} {
		t.Run(kind, func(t *testing.T) {
			cfg, calls := builtinGateway(t, func(w http.ResponseWriter, r *http.Request) {
				acceptBuiltinTunnel(w, r)
				request, err := http.ReadRequest(bufio.NewReader(r.Body))
				if err != nil {
					t.Error(err)
					return
				}
				defer request.Body.Close()
				_, _ = io.Copy(io.Discard, request.Body)
				if request.Header.Get("Proxy-Authorization") != "" {
					t.Error("inner credential leak")
				}
				_, _ = io.WriteString(w, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
				w.(http.Flusher).Flush()
				_, _ = io.Copy(io.Discard, r.Body)
			})
			var peer *proxypeer.Proxy
			if kind == "http" {
				peer = proxypeer.HTTP(t, cfg.GatewayAddress, false)
			} else {
				peer = proxypeer.SOCKS(t, cfg.GatewayAddress, false)
			}
			ctx := netconfig.WithEnvironment(context.Background(), []string{"ALL_PROXY=" + peer.URL, "NO_PROXY=" + cfg.Endpoint + ":443"})
			ctx = context.WithValue(ctx, helperNetworkPortKey{}, "9443")
			cfg.GatewayAddress = cfg.Endpoint + ":9443"
			_, local := startBuiltinTestHelperContext(t, cfg, ctx)
			_, err := io.WriteString(local, "POST /v3/pipeline HTTP/1.1\r\nHost: localhost\r\nContent-Length: 2\r\n\r\n{}")
			if err != nil {
				t.Fatal(err)
			}
			response, err := http.ReadResponse(bufio.NewReader(local), &http.Request{Method: "POST"})
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || string(body) != "ok" {
				t.Fatalf("body: %s %v", body, err)
			}
			if calls.Load() != 1 || len(peer.Requests()) != 1 || peer.Requests()[0].Address != cfg.Endpoint+":9443" {
				t.Fatal("helper did not use selected proxy")
			}
			prepared, err := (SQLDAdapter{}).Prepare(LocalEndpoint{Network: "tcp", URL: "http://127.0.0.1:12345", Address: "127.0.0.1:12345"}, ProfileHranaHTTP, []string{"turso", "db", "shell", "https://" + cfg.Endpoint}, netconfig.HelperEnvironment(ctx, []string{"PATH=/bin"}))
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range prepared.Env {
				if strings.Contains(strings.ToLower(strings.SplitN(entry, "=", 2)[0]), "proxy") {
					t.Fatal("native child received proxy environment")
				}
			}
		})
	}
}
