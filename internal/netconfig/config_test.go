package netconfig

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/tianacloud/cli/internal/clientconfig"
	"github.com/tianacloud/cli/internal/testutil/proxypeer"
)

func TestEnvironmentSnapshotAndHelperSeparation(t *testing.T) {
	environment := []string{"ALL_PROXY=socks5h://user:pass@proxy.example.test:1080", "all_proxy=http://lower.example.test:80", "HTTPS_PROXY=", "https_proxy=http://specific.example.test:8080", "NO_PROXY=local.example.test", "REQUEST_METHOD=GET", "UNRELATED=secret"}
	ctx := WithEnvironment(context.Background(), environment)
	environment[0] = "ALL_PROXY=http://changed.example.test"
	p, err := Policy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for raw, want := range map[string]string{"https://remote.example.test": "http://specific.example.test:8080", "http://remote.example.test": "socks5h://user:pass@proxy.example.test:1080", "https://local.example.test": ""} {
		u, _ := url.Parse(raw)
		got, err := p.ProxyForURL(u)
		if err != nil {
			t.Fatal(err)
		}
		s := ""
		if got != nil {
			s = got.String()
		}
		if s != want {
			t.Fatalf("route %s: %s", raw, s)
		}
	}
	child := HelperEnvironment(ctx, []string{"PATH=/bin", "https_proxy=http://stale.example.test", "ALL_PROXY=http://stale.example.test"})
	joined := strings.Join(child, "\n")
	if strings.Contains(joined, "stale") || strings.Contains(joined, "UNRELATED") || strings.Contains(joined, "changed") || !strings.Contains(joined, "ALL_PROXY=socks5h://user:pass@proxy.example.test:1080") || !strings.Contains(joined, "REQUEST_METHOD=1") {
		t.Fatal("helper snapshot was not normalized")
	}
}

func TestHTTPTransportUsesAllProxyAndTargetTLS(t *testing.T) {
	const host = "mgr.example.test"
	server, roots := proxypeer.TLS(t, host, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS.ServerName != host || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("target TLS/credentials")
		}
		w.Write([]byte("ok"))
	}))
	for _, kind := range []string{"http", "socks5h"} {
		t.Run(kind, func(t *testing.T) {
			var peer *proxypeer.Proxy
			if kind == "http" {
				peer = proxypeer.HTTP(t, server.Listener.Addr().String(), false)
			} else {
				peer = proxypeer.SOCKS(t, server.Listener.Addr().String(), true)
			}
			u, _ := url.Parse(peer.URL)
			u.User = url.UserPassword("user", "pass")
			ctx := WithEnvironment(context.Background(), []string{"ALL_PROXY=" + u.String()})
			ctx = clientconfig.WithTrust(ctx, &clientconfig.Trust{Roots: roots})
			defer Close(ctx)
			transport, err := NewTransport(ctx)
			if err != nil {
				t.Fatal(err)
			}
			c := &http.Client{Transport: transport}
			response, err := c.Get("https://" + host + "/api/v1/test")
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || string(body) != "ok" {
				t.Fatalf("body: %s %v", body, err)
			}
			r := peer.Requests()
			if len(r) != 1 || r[0].Address != host+":443" {
				t.Fatalf("proxy route: %+v", r)
			}
		})
	}
}

func TestHTTPTransportUsesSpecificProxyWithoutFallback(t *testing.T) {
	server, roots := proxypeer.TLS(t, "mgr.example.test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	for _, reject := range []bool{false, true} {
		primary := proxypeer.HTTP(t, server.Listener.Addr().String(), false)
		primary.RequireAuth = reject
		fallback := proxypeer.SOCKS(t, server.Listener.Addr().String(), false)
		ctx := WithEnvironment(context.Background(), []string{"HTTPS_PROXY=" + primary.URL, "ALL_PROXY=" + fallback.URL})
		ctx = clientconfig.WithTrust(ctx, &clientconfig.Trust{Roots: roots})
		defer Close(ctx)
		transport, err := NewTransport(ctx)
		if err != nil {
			t.Fatal(err)
		}
		client := &http.Client{Transport: transport}
		response, err := client.Get("https://mgr.example.test/")
		if response != nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
		if (err != nil) != reject || len(primary.Requests()) != 1 || len(fallback.Requests()) != 0 {
			t.Fatalf("scheme-specific proxy priority/fallback: reject=%t err=%v primary=%d fallback=%d", reject, err, len(primary.Requests()), len(fallback.Requests()))
		}
	}
}
