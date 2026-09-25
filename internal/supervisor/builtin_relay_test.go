package supervisor

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"github.com/tianacloud/cli/internal/diagnostics"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const builtinTestToken = "tia_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func builtinGateway(t *testing.T, handler http.HandlerFunc) (HelperConfig, *atomic.Int32) {
	t.Helper()
	cfg := builtinTestConfig()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{cfg.Endpoint}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "CONNECT" || r.Host != cfg.Endpoint+":443" || r.Header.Get("Proxy-Authorization") != "Bearer "+builtinTestToken || r.ProtoMajor != 2 || r.TLS.Version != tls.VersionTLS13 || r.TLS.ServerName != cfg.Endpoint {
			t.Error("invalid CONNECT/auth/TLS authority")
		}
		handler(w, r)
	}))
	s.EnableHTTP2 = true
	s.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	s.StartTLS()
	t.Cleanup(s.Close)
	cfg.RootCertDER = [][]byte{der}
	cfg.GatewayAddress = s.Listener.Addr().String()
	return cfg, &calls
}
func startBuiltinTestHelper(t *testing.T, cfg HelperConfig) (*ProcessHelper, net.Conn) {
	return startBuiltinTestHelperContext(t, cfg, context.Background())
}

func startBuiltinTestHelperContext(t *testing.T, cfg HelperConfig, parent context.Context) (*ProcessHelper, net.Conn) {
	t.Helper()
	p, _ := builtinTestPeerContext(t, parent)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := p.Handshake(ctx, []uint16{3}, "relay-test"); err != nil {
		t.Fatal(err)
	}
	if err := p.Configure(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	token, err := ParseToken([]byte(builtinTestToken))
	if err != nil {
		t.Fatal(err)
	}
	defer token.Destroy()
	if err = p.DeliverCredential(ctx, token); err != nil {
		t.Fatal(err)
	}
	bound, err := p.WaitBound(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	id, err := newChildIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err = p.ChildStarted(ctx, id); err != nil {
		t.Fatal(err)
	}
	local, err := net.DialTimeout("tcp", bound.Endpoint.Address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { local.Close() })
	local.SetDeadline(time.Now().Add(4 * time.Second))
	return p, local
}
func acceptBuiltinTunnel(w http.ResponseWriter, r *http.Request) {
	w.Header()["Date"] = nil
	w.Header()["Content-Type"] = nil
	w.Header().Set("tiana-tunnel-version", "1")
	w.Header().Set("tiana-request-id", r.Header.Get("tiana-request-id"))
	w.Header().Set("tiana-auth-mode", "TOKEN_REQUIRED")
	w.WriteHeader(200)
	w.(http.Flusher).Flush()
}

func TestBuiltinRelayBarrierAndOpaqueBytes(t *testing.T) {
	for _, ws := range []bool{false, true} {
		t.Run(map[bool]string{false: "http", true: "websocket"}[ws], func(t *testing.T) {
			request := "POST /v3/pipeline HTTP/1.1\r\nHost: local\r\nContent-Length: 6\r\n\r\nSECRET"
			profile := ProfileHranaHTTP
			if ws {
				request = "GET / HTTP/1.1\r\nHost: local\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n\x81\x00"
				profile = ProfileHranaWebSocket
			}
			received := make(chan string, 1)
			entered := make(chan struct{})
			allow := make(chan struct{})
			early := make(chan struct{}, 1)
			cfg, calls := builtinGateway(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("tiana-database-protocol") != profile {
					t.Error("wrong selected profile")
				}
				close(entered)
				go func() {
					buf := make([]byte, len(request))
					_, err := io.ReadFull(r.Body, buf)
					if err != nil {
						received <- "read error"
						return
					}
					early <- struct{}{}
					received <- string(buf)
				}()
				select {
				case <-allow:
				case <-r.Context().Done():
					return
				}
				acceptBuiltinTunnel(w, r)
				// Half-close: read EOF only after every prefix byte was delivered.
				select {
				case got := <-received:
					if got != request {
						t.Error("request bytes changed or lost")
					}
				case <-r.Context().Done():
					return
				}
				io.WriteString(w, "reply")
				w.(http.Flusher).Flush()
				io.Copy(io.Discard, r.Body)
			})
			p, local := startBuiltinTestHelper(t, cfg)
			if _, err := io.WriteString(local, request); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("no CONNECT")
			}
			select {
			case <-early:
				t.Fatal("database bytes sent before CONNECT 200")
			case <-time.After(40 * time.Millisecond):
			}
			close(allow)
			// The local upload FIN must not discard the response direction.
			local.(*net.TCPConn).CloseWrite()
			reply, err := io.ReadAll(local)
			if err != nil || string(reply) != "reply" {
				p.Drain(context.Background())
				t.Fatalf("reply=%q err=%v failure=%v", reply, err, p.WaitStopped(context.Background()))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err = p.Drain(ctx); err != nil {
				t.Fatal(err)
			}
			if err = p.WaitStopped(ctx); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatalf("CONNECT count=%d", calls.Load())
			}
		})
	}
}

func TestBuiltinRelayRefusesWithoutSQLOrRetry(t *testing.T) {
	for _, mode := range []string{"refused", "tls", "invalid", "profile", "bad200"} {
		t.Run(mode, func(t *testing.T) {
			cfg, calls := builtinGateway(t, func(w http.ResponseWriter, r *http.Request) {
				if mode == "bad200" {
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}
				w.Header().Set("tiana-error-code", "ACCESS_DENIED")
				w.WriteHeader(403)
			})
			if mode == "tls" {
				cfg.RootCertDER = nil
			}
			if mode == "profile" {
				cfg.SelectionMode = SelectionModeExplicitProfile
				cfg.ExplicitProfile = ProfileHranaWebSocket
			}
			p, local := startBuiltinTestHelper(t, cfg)
			request := "POST /v2/pipeline HTTP/1.1\r\nHost: local\r\n\r\nSECRET"
			if mode == "invalid" {
				request = "GET /bad HTTP/1.1\r\nHost: local\r\n\r\n"
			}
			io.WriteString(local, request)
			io.ReadAll(local)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := p.Drain(ctx); err != nil {
				t.Fatal(err)
			}
			err := p.WaitStopped(ctx)
			var failure SessionFailureError
			if !errors.As(err, &failure) || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("missing safe failure: %v", err)
			}
			want := "LOCAL_REQUEST_REJECTED"
			var wantCalls int32
			if mode == "refused" {
				want = "ACCESS_DENIED"
				wantCalls = 1
			}
			if mode == "tls" {
				want = "GATEWAY_TLS"
			}
			if mode == "bad200" {
				want = "TUNNEL_INTERRUPTED"
				wantCalls = 1
				if failure.Phase != SessionFailureAfterConnect {
					t.Errorf("lost CONNECT 200 boundary: %+v", failure)
				}
			}
			if failure.Code != want || calls.Load() != wantCalls {
				t.Fatalf("failure=%+v calls=%d", failure, calls.Load())
			}
		})
	}
}

func TestBuiltinRelayDrainActiveAndControlEOF(t *testing.T) {
	for _, eof := range []bool{false, true} {
		t.Run(map[bool]string{false: "drain", true: "eof"}[eof], func(t *testing.T) {
			entered := make(chan struct{})
			closed := make(chan struct{})
			cfg, _ := builtinGateway(t, func(w http.ResponseWriter, r *http.Request) {
				acceptBuiltinTunnel(w, r)
				close(entered)
				<-r.Context().Done()
				close(closed)
			})
			p, local := startBuiltinTestHelper(t, cfg)
			io.WriteString(local, "POST /v3/pipeline HTTP/1.1\r\nHost: local\r\n\r\n")
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("missing CONNECT")
			}
			if eof {
				p.Close()
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				if err := p.Drain(ctx); err != nil {
					t.Fatal(err)
				}
				if err := p.WaitStopped(ctx); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("upstream leaked after shutdown")
			}
			if _, err := local.Read(make([]byte, 1)); err == nil {
				t.Fatal("local socket still open")
			}
		})
	}
}

func TestBuiltinRelayBoundsIdleConnections(t *testing.T) {
	cfg, calls := builtinGateway(t, func(http.ResponseWriter, *http.Request) { t.Error("idle local connection reached Gateway") })
	p, first := startBuiltinTestHelper(t, cfg)
	peers := []net.Conn{first}
	for i := 1; i < 32; i++ {
		c, err := net.DialTimeout("tcp", first.RemoteAddr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		peers = append(peers, c)
		defer c.Close()
	}
	excess, err := net.DialTimeout("tcp", first.RemoteAddr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer excess.Close()
	excess.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if _, err = excess.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("33rd connection was not refused: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err = p.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if err = p.WaitStopped(ctx); err != nil {
		t.Fatal(err)
	}
	for _, c := range peers {
		c.SetReadDeadline(time.Now().Add(time.Second))
		if _, err = c.Read(make([]byte, 1)); err != io.EOF {
			t.Fatalf("idle peer leaked: %v", err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("idle connections performed CONNECT")
	}
}

func TestBuiltinRelayFailureDoesNotCancelSibling(t *testing.T) {
	var n atomic.Int32
	cfg, calls := builtinGateway(t, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.Header().Set("tiana-error-code", "ACCESS_DENIED")
			w.WriteHeader(403)
			return
		}
		acceptBuiltinTunnel(w, r)
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != "POST /v2/pipeline HTTP/1.1\r\nHost: local\r\n\r\n" {
			t.Error("healthy sibling bytes changed")
		}
		io.WriteString(w, "ok")
		w.(http.Flusher).Flush()
	})
	p, failed := startBuiltinTestHelper(t, cfg)
	request := "POST /v2/pipeline HTTP/1.1\r\nHost: local\r\n\r\n"
	io.WriteString(failed, request)
	io.ReadAll(failed)
	healthy, err := net.DialTimeout("tcp", failed.RemoteAddr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer healthy.Close()
	healthy.SetDeadline(time.Now().Add(2 * time.Second))
	io.WriteString(healthy, request)
	healthy.(*net.TCPConn).CloseWrite()
	result, err := io.ReadAll(healthy)
	if err != nil || string(result) != "ok" {
		t.Fatalf("healthy sibling lost: %q %v", result, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err = p.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	var failure SessionFailureError
	if err = p.WaitStopped(ctx); !errors.As(err, &failure) || failure.Code != "ACCESS_DENIED" {
		t.Fatalf("first failure lost: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("unexpected retry: %d", calls.Load())
	}
}

type diagnosticLines chan string

func (lines diagnosticLines) Write(p []byte) (int, error) { lines <- string(p); return len(p), nil }

func TestBuiltinConnectionDiagnosticPrecedesRejectedGatewayRequest(t *testing.T) {
	lines := make(diagnosticLines, 2)
	received := make(chan string, 1)
	cfg, _ := builtinGateway(t, func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("tiana-request-id")
		select {
		case line := <-lines:
			if line != "Request ID: "+id+" (connect)\n" {
				t.Errorf("diagnostic ID differs from CONNECT: %q", line)
			}
		default:
			t.Error("request reached Gateway before connection diagnostic")
		}
		received <- id
		w.WriteHeader(http.StatusForbidden)
	})
	_, local := startBuiltinTestHelperContext(t, cfg, diagnostics.WithWriter(context.Background(), lines))
	if _, err := io.WriteString(local, "POST /v2/pipeline HTTP/1.1\r\nHost: local\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-received:
		if !strings.HasPrefix(id, "req-") {
			t.Fatal("missing connection ID")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Gateway did not receive connection")
	}
}
