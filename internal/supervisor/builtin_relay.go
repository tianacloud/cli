package supervisor

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	tiana "github.com/tianacloud/sdk-go"
)

const builtinSessionLimit = 32

type builtinRelay struct {
	ctx                     context.Context
	cancel                  context.CancelFunc
	listener                *net.TCPListener
	client                  *tiana.Client
	profile                 string
	mu                      sync.Mutex
	active                  map[*net.TCPConn]struct{}
	first                   *SessionFailureError
	workers                 sync.WaitGroup
	ready, accepted, failed chan struct{}
	stopOnce                sync.Once
}

func newBuiltinRelay(ctx context.Context, l *net.TCPListener, c *tiana.Client, profile string) *builtinRelay {
	ctx, cancel := context.WithCancel(ctx)
	r := &builtinRelay{ctx: ctx, cancel: cancel, listener: l, client: c, profile: profile, active: make(map[*net.TCPConn]struct{}), ready: make(chan struct{}), accepted: make(chan struct{}), failed: make(chan struct{})}
	go r.accept()
	return r
}
func (r *builtinRelay) start() { close(r.ready) }
func (r *builtinRelay) accept() {
	defer close(r.accepted)
	select {
	case <-r.ready:
	case <-r.ctx.Done():
		return
	}
	for {
		conn, err := r.listener.AcceptTCP()
		if err != nil {
			if r.ctx.Err() == nil {
				r.record(SessionFailureError{Phase: SessionFailureLocalRequest, Code: "LOCAL_LISTENER_FAILED"})
				close(r.failed)
			}
			return
		}
		r.mu.Lock()
		if len(r.active) >= builtinSessionLimit || r.ctx.Err() != nil {
			r.mu.Unlock()
			conn.Close()
			continue
		}
		r.active[conn] = struct{}{}
		r.workers.Add(1)
		r.mu.Unlock()
		go func() {
			defer r.workers.Done()
			defer conn.Close()
			defer func() { r.mu.Lock(); delete(r.active, conn); r.mu.Unlock() }()
			if failure := r.serve(conn); failure != nil {
				r.record(*failure)
			}
		}()
	}
}
func (r *builtinRelay) record(f SessionFailureError) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.first == nil && r.ctx.Err() == nil {
		r.first = &f
	}
}
func (r *builtinRelay) failure() *SessionFailureError {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.first == nil {
		return nil
	}
	f := *r.first
	return &f
}
func (r *builtinRelay) stop() {
	r.stopOnce.Do(func() {
		r.cancel()
		r.listener.Close()
		r.mu.Lock()
		for conn := range r.active {
			conn.Close()
		}
		r.mu.Unlock()
		r.client.Close()
		<-r.accepted
		r.workers.Wait()
	})
}
func (r *builtinRelay) serve(local *net.TCPConn) *SessionFailureError {
	local.SetReadDeadline(time.Now().Add(SQLDClassifierDeadline))
	prefix, profile, err := readBuiltinPrefix(local)
	if err != nil {
		return &SessionFailureError{Phase: SessionFailureLocalRequest, Code: "LOCAL_REQUEST_REJECTED"}
	}
	defer clear(prefix)
	local.SetReadDeadline(time.Time{})
	if r.profile != "" && profile != r.profile {
		return &SessionFailureError{Phase: SessionFailureLocalRequest, Code: "LOCAL_REQUEST_REJECTED"}
	}
	tunnel, err := r.client.Connect(r.ctx, tiana.Profile(profile))
	if err != nil {
		f := builtinConnectFailure(err)
		return &f
	}
	defer tunnel.Close()
	// The SDK returns only after validated CONNECT 200. On any write failure the
	// prefix is consumed, cleared and never offered to another connection.
	if err = writeAll(tunnel, prefix); err != nil {
		return &SessionFailureError{Phase: SessionFailureAfterConnect, Code: "TUNNEL_INTERRUPTED"}
	}
	clear(prefix)
	type copied struct {
		upload bool
		err    error
	}
	done := make(chan copied, 2)
	copyDirection := func(dst io.Writer, src io.Reader, upload bool) {
		buffer := make([]byte, 32*1024)
		defer clear(buffer)
		_, err := io.CopyBuffer(dst, src, buffer)
		if err == nil {
			if upload {
				err = tunnel.CloseWrite()
			} else {
				err = local.CloseWrite()
			}
		}
		done <- copied{upload, err}
	}
	go copyDirection(tunnel, local, true)
	go copyDirection(local, tunnel, false)
	first := <-done
	if first.err != nil {
		local.Close()
		tunnel.Close()
	}
	second := <-done
	if first.err == nil && second.err == nil {
		return nil
	}
	// SDK errors identify the upstream tunnel. Native local closure is distinct
	// so a successful native exit can retain its normal status.
	failure := SessionFailureError{Phase: SessionFailureAfterConnect, Code: "TUNNEL_INTERRUPTED"}
	var sdkErr *tiana.Error
	err = first.err
	if err == nil {
		err = second.err
	}
	if !errors.As(err, &sdkErr) {
		failure.Code = "LOCAL_CLIENT_CLOSED"
	}
	return &failure
}
func builtinConnectFailure(err error) SessionFailureError {
	f := SessionFailureError{Phase: SessionFailureBeforeConnect, Code: "GATEWAY_PROTOCOL"}
	var sdk *tiana.Error
	if !errors.As(err, &sdk) {
		return f
	}
	if sdk.Committed {
		return SessionFailureError{Phase: SessionFailureAfterConnect, Code: "TUNNEL_INTERRUPTED"}
	}
	switch sdk.Kind {
	case tiana.Configuration:
		f.Code = "CLIENT_CONFIGURATION"
	case tiana.TCP:
		f.Code = "GATEWAY_UNREACHABLE"
	case tiana.TLS:
		f.Code = "GATEWAY_TLS"
	case tiana.Timeout:
		f.Code = "GATEWAY_TIMEOUT"
	case tiana.Limit:
		f.Code = "CONNECTION_LIMIT"
	case tiana.Gateway:
		if sdk.Status >= 100 && sdk.Status <= 599 {
			f.Status = uint16(sdk.Status)
		}
		switch sdk.Code {
		case "AUTH_REQUIRED", "ACCESS_DENIED", "AUTHORIZATION_EXPIRED", "CALLER_DEADLINE", "ENDPOINT_MISMATCH", "CONNECTION_LIMIT", "POLICY_UNAVAILABLE", "INSTANCE_UNAVAILABLE", "ACTIVATION_TIMEOUT":
			f.Code = sdk.Code
		}
		if sdk.RetryAfter > 0 {
			f.RetryAfterMS = uint32(min(sdk.RetryAfter.Milliseconds(), int64(1<<32-1)))
		}
	}
	return f
}
