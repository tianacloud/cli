package gitremote

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tianacloud/cli/internal/diagnostics"
	"github.com/tianacloud/cli/internal/supervisor"
)

type httpTunnel struct {
	io.ReadCloser
	writer    *io.PipeWriter
	reader    *io.PipeReader
	cancel    context.CancelFunc
	transport *http.Transport
	conn      net.Conn
	requestID string
}

func (t *httpTunnel) RequestID() string { return t.requestID }

func (t *httpTunnel) Write(p []byte) (int, error) { return t.writer.Write(p) }
func (t *httpTunnel) CloseWrite() error           { return t.writer.Close() }
func (t *httpTunnel) Close() error {
	t.cancel()
	t.writer.Close()
	t.reader.Close()
	t.ReadCloser.Close()
	t.transport.CloseIdleConnections()
	return t.conn.Close()
}

func connect(ctx context.Context, repo supervisor.Endpoint) (result tunnel, err error) {
	config, err := configurationWithContext(ctx, repo)
	if err != nil {
		return nil, err
	}
	if config.token != nil {
		defer config.token.Destroy()
	}
	random := make([]byte, 18)
	if _, err = rand.Read(random); err != nil {
		return nil, errors.New("request ID generation failed")
	}
	requestID := "req-" + base64.RawURLEncoding.EncodeToString(random)
	diagnostics.Write(ctx, "git", requestID)
	if identity, ok := ctx.Value(requestIdentityKey{}).(*requestIdentity); ok {
		identity.set(requestID)
	}
	defer func() {
		if err != nil {
			err = gitConnectionError(err, requestID)
		}
	}()
	ctx, cancel := context.WithCancel(ctx)
	reader, writer := io.Pipe()
	dialCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	raw, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", config.address)
	if err != nil {
		stop()
		cancel()
		reader.Close()
		writer.Close()
		var dnsError *net.DNSError
		if errors.As(err, &dnsError) {
			return nil, errors.New("Gateway DNS resolution failed")
		}
		if errors.Is(err, syscall.ECONNREFUSED) {
			return nil, errors.New("Gateway TCP connection refused")
		}
		return nil, errors.New("Gateway TCP connection failed")
	}
	connection := tls.Client(raw, config.tls)
	err = connection.HandshakeContext(dialCtx)
	stop()
	if err != nil || connection.ConnectionState().NegotiatedProtocol != "h2" {
		connection.Close()
		cancel()
		reader.Close()
		writer.Close()
		return nil, errors.New("Gateway TLS verification/handshake or HTTP/2 negotiation failed")
	}
	var dialed atomic.Bool
	transport := &http.Transport{
		ForceAttemptHTTP2: true, DisableCompression: true, MaxResponseHeaderBytes: 16 * 1024,
		// Own exactly one pre-established TLS connection. No proxy, second dial,
		// HTTP/1 fallback, or pool shared with another operation.
		DialTLSContext: func(context.Context, string, string) (net.Conn, error) {
			if dialed.Swap(true) {
				return nil, errors.New("Git tunnel reconnection is disabled")
			}
			return connection, nil
		},
	}

	success := false
	defer func() {
		if !success {
			cancel()
			writer.Close()
			reader.Close()
			transport.CloseIdleConnections()
			if connection != nil {
				connection.Close()
			}
		}
	}()
	authority := net.JoinHostPort(repo.Hostname(), "443")
	request := (&http.Request{Method: http.MethodConnect, URL: &url.URL{Scheme: "https", Host: authority}, Host: authority, Header: make(http.Header), Body: reader, ContentLength: -1}).WithContext(ctx)
	request.Header.Set("Tiana-Tunnel-Version", "1")
	request.Header.Set("Tiana-Database-Protocol", "git")
	request.Header.Set("Tiana-Request-Id", requestID)
	request.Header["User-Agent"] = nil
	if config.token != nil {
		request.Header.Set("Proxy-Authorization", "Bearer "+string(config.token.BytesForHandoff()))
	}
	// RoundTrip returns on response HEADERS while request Body remains open.
	// The pipe cannot yield any native DATA until this response is validated.
	// HTTP Transport's header timeout starts after upload EOF, which cannot
	// happen before CONNECT 200. Bound setup on the owned socket instead.
	if err := connection.SetDeadline(time.Now().Add(60 * time.Second)); err != nil {
		return nil, errors.New("cannot set CONNECT deadline")
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		return nil, errors.New("Gateway CONNECT failed")
	}
	if response.StatusCode != 200 {
		response.Body.Close()
		return nil, fmt.Errorf("Gateway CONNECT rejected (HTTP %d)", response.StatusCode)
	}
	valid := validSuccess(response, requestID)
	if !valid {
		response.Body.Close()
		return nil, errors.New("invalid Gateway CONNECT success envelope")
	}
	if err := connection.SetDeadline(time.Time{}); err != nil {
		response.Body.Close()
		return nil, errors.New("cannot clear CONNECT deadline")
	}
	success = true
	return &httpTunnel{ReadCloser: response.Body, writer: writer, reader: reader, cancel: cancel, transport: transport, conn: connection, requestID: requestID}, nil
}

func validSuccess(response *http.Response, requestID string) bool {
	valid := response.StatusCode == 200 && response.ProtoMajor == 2 && len(response.Header) == 3
	for name, want := range map[string]string{"Tiana-Tunnel-Version": "1", "Tiana-Request-Id": requestID} {
		values := response.Header.Values(name)
		valid = valid && len(values) == 1 && values[0] == want
	}
	modes := response.Header.Values("Tiana-Auth-Mode")
	valid = valid && len(modes) == 1 && (modes[0] == "DISABLED" || modes[0] == "TOKEN_REQUIRED")
	return valid
}
