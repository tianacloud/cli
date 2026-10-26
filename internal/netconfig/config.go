// Package netconfig owns invocation-scoped, explicit network configuration.
package netconfig

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/tianacloud/cli/internal/clientconfig"
	"github.com/tianacloud/sdk-go/outbound"
)

type key struct{}
type snapshot struct {
	config     outbound.Config
	once       sync.Once
	policy     *outbound.Policy
	err        error
	mu         sync.Mutex
	transports []*http.Transport
}

// WithEnvironment snapshots only reviewed proxy settings. It is lazy: local
// commands do not validate or initialize networking. Existing snapshots win.
func WithEnvironment(ctx context.Context, environment []string) context.Context {
	if _, ok := ctx.Value(key{}).(*snapshot); ok {
		return ctx
	}
	values := map[string]string{}
	for _, entry := range environment {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if runtime.GOOS == "windows" {
			name = strings.ToUpper(name)
		}
		switch name {
		case "HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy", "REQUEST_METHOD":
			if _, seen := values[name]; !seen || (runtime.GOOS == "windows" && values[name] == "") {
				values[name] = value
			}
		}
	}
	get := func(a, b string) string {
		if values[a] != "" {
			return values[a]
		}
		return values[b]
	}
	s := &snapshot{config: outbound.Config{HTTPProxy: get("HTTP_PROXY", "http_proxy"), HTTPSProxy: get("HTTPS_PROXY", "https_proxy"), AllProxy: get("ALL_PROXY", "all_proxy"), NoProxy: get("NO_PROXY", "no_proxy"), CGI: values["REQUEST_METHOD"] != ""}}
	return context.WithValue(ctx, key{}, s)
}

func state(ctx context.Context) *snapshot {
	if s, ok := ctx.Value(key{}).(*snapshot); ok {
		return s
	}
	return WithEnvironment(ctx, os.Environ()).Value(key{}).(*snapshot)
}

func Policy(ctx context.Context) (*outbound.Policy, error) {
	var roots *x509.CertPool
	if trust := clientconfig.FromContext(ctx); trust != nil {
		roots = trust.Roots
	}
	return PolicyWithRoots(ctx, roots)
}

// PolicyWithRoots supports the helper's trust decoded from private control.
func PolicyWithRoots(ctx context.Context, roots *x509.CertPool) (*outbound.Policy, error) {
	s := state(ctx)
	s.once.Do(func() { c := s.config; c.ProxyRootCAs = roots; s.policy, s.err = outbound.New(c) })
	return s.policy, s.err
}

// NewTransport owns an independent pool while sharing immutable routing.
func NewTransport(ctx context.Context) (*http.Transport, error) {
	p, err := Policy(ctx)
	if err != nil {
		return nil, err
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = func(r *http.Request) (*url.URL, error) { return p.ProxyForURL(r.URL) }
	if trust := clientconfig.FromContext(ctx); trust != nil {
		t.TLSClientConfig = &tls.Config{RootCAs: trust.Roots, MinVersion: tls.VersionTLS12}
	}
	if s, ok := ctx.Value(key{}).(*snapshot); ok {
		s.mu.Lock()
		s.transports = append(s.transports, t)
		s.mu.Unlock()
	}
	return t, nil
}

// Close releases idle sockets created by HTTP factories in this invocation.
func Close(ctx context.Context) {
	if s, ok := ctx.Value(key{}).(*snapshot); ok {
		s.mu.Lock()
		transports := s.transports
		s.transports = nil
		s.mu.Unlock()
		for _, t := range transports {
			t.CloseIdleConnections()
		}
	}
}

// HelperEnvironment appends the canonical original settings, not the resolved
// fallback, to a separately scrubbed helper environment. Native children must
// never receive this environment.
func HelperEnvironment(ctx context.Context, base []string) []string {
	s := state(ctx)
	result := make([]string, 0, len(base)+5)
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(name) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "REQUEST_METHOD":
			continue
		}
		result = append(result, entry)
	}
	for _, entry := range []struct{ name, value string }{{"HTTP_PROXY", s.config.HTTPProxy}, {"HTTPS_PROXY", s.config.HTTPSProxy}, {"ALL_PROXY", s.config.AllProxy}, {"NO_PROXY", s.config.NoProxy}} {
		if entry.value != "" {
			result = append(result, entry.name+"="+entry.value)
		}
	}
	if s.config.CGI {
		result = append(result, "REQUEST_METHOD=1")
	}
	return result
}
