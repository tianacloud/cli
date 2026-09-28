package supervisor

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func builtinTestConfig() HelperConfig {
	return HelperConfig{Endpoint: "ep-00000000000000000000000000.db.example.test", AdapterID: SQLDAdapterID,
		AllowedProfile: []string{ProfileHranaHTTP, ProfileHranaWebSocket}, SelectionMode: SelectionModeBoundedHTTPHeaderClassifier,
		Deadline: SQLDClassifierDeadline, HeaderLimit: SQLDHeaderLimit, FieldLimit: SQLDFieldLimit, Pre200Limit: SQLDPre200Limit, UseWebPKIRoots: true}
}

func builtinTestPeer(t *testing.T) (*ProcessHelper, <-chan error) {
	return builtinTestPeerContext(t, context.Background())
}

func builtinTestPeerContext(t *testing.T, ctx context.Context) (*ProcessHelper, <-chan error) {
	t.Helper()
	if path := os.Getenv("TIANA_TEST_BUILTIN_CLI"); path != "" {
		helper, err := launchHelperCommand(ctx, exec.Command(path, BuiltinHelperArgument), DefaultCredentialSource(), nil)
		if err != nil {
			t.Fatal(err)
		}
		p := helper.(*ProcessHelper)
		t.Cleanup(func() { p.Close() })
		return p, nil
	}
	in, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	read, out, err := os.Pipe()
	if err != nil {
		in.Close()
		write.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- ServeBuiltinHelper(ctx, in, out); out.Close() }()
	p := &ProcessHelper{write: write, read: read, reader: bufio.NewReader(read)}
	t.Cleanup(func() {
		p.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("helper leaked after control EOF")
		}
	})
	return p, done
}

func TestBuiltinHelperLifecycle(t *testing.T) {
	p, _ := builtinTestPeer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	caps, err := p.Handshake(ctx, []uint16{3}, "native-test")
	if err != nil || !caps.SQLDAdapter || !caps.SQLDExplicitProfile {
		t.Fatal(caps, err)
	}
	if err = p.Configure(ctx, builtinTestConfig()); err != nil {
		t.Fatal(err)
	}
	if err = p.DeliverCredential(ctx, nil); err != nil {
		t.Fatal(err)
	}
	b, err := p.WaitBound(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if b.Endpoint.SecurityLevel != SecurityLoopbackUnisolated {
		t.Fatal("false isolation claim")
	}
	if _, err = p.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", b.Endpoint.Address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("POST /v2/pipeline HTTP/1.1\r\nHost: local\r\n\r\n"))
	conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	var buf [1]byte
	if _, err = conn.Read(buf[:]); err == nil {
		t.Fatal("accepted before child handoff")
	}
	identity, err := newChildIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err = p.ChildStarted(ctx, identity); err != nil {
		t.Fatal(err)
	}
	if err = p.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if err = p.WaitStopped(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestBuiltinClassifierPreservesPrefix(t *testing.T) {
	ws := "GET / HTTP/1.1\r\nHost: local\r\nConnection: keep-alive, Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n"
	for _, tc := range []struct{ request, profile string }{
		{"POST /v2/pipeline HTTP/1.1\r\nHost: local\r\nContent-Length: 6\r\n\r\nSECRET", ProfileHranaHTTP},
		{"POST /v3/pipeline HTTP/1.1\r\nHost: local\r\n\r\n", ProfileHranaHTTP},
		{"POST /v3/cursor HTTP/1.1\r\nHost: local\r\n\r\n", ProfileHranaHTTP}, {ws, ProfileHranaWebSocket},
	} {
		got, profile, err := readBuiltinPrefix(strings.NewReader(tc.request))
		if err != nil || profile != tc.profile || !bytes.Equal(got, []byte(tc.request)) {
			t.Fatalf("profile=%s err=%v", profile, err)
		}
	}
	for _, input := range []string{"GET / HTTP/1.1\r\nHost: local\r\n\r\n", "POST /v2/pipeline HTTP/1.0\r\nHost: local\r\n\r\n", "POST /v2/pipeline HTTP/1.1\r\nHost: x\r\nHost: y\r\n\r\n", strings.Repeat("S", SQLDHeaderLimit), "POST /v2/pipeline HTTP/1.1\r\nHost: local\r\n" + strings.Repeat("X: y\r\n", 64) + "\r\n", strings.Replace(ws, "13", "12", 1)} {
		if _, _, err := readBuiltinPrefix(strings.NewReader(input)); err == nil {
			t.Fatal("accepted invalid header")
		}
	}
}

func TestBuiltinDrainBeforeChildAndRejectBadIdentity(t *testing.T) {
	for _, mode := range []string{"drain", "identity", "eof"} {
		t.Run(mode, func(t *testing.T) {
			p, _ := builtinTestPeer(t)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if _, err := p.Handshake(ctx, []uint16{3}, "pre-child"); err != nil {
				t.Fatal(err)
			}
			if err := p.Configure(ctx, builtinTestConfig()); err != nil {
				t.Fatal(err)
			}
			if err := p.DeliverCredential(ctx, nil); err != nil {
				t.Fatal(err)
			}
			b, err := p.WaitBound(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = p.WaitReady(ctx); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "drain":
				if err = p.Drain(ctx); err != nil {
					t.Fatal(err)
				}
				if err = p.WaitStopped(ctx); err != nil {
					t.Fatal(err)
				}
			case "identity":
				if err = p.ChildStarted(ctx, ChildIdentity{PID: os.Getpid(), StartTime: ^uint64(0)}); err == nil {
					t.Fatal("accepted invalid owner identity")
				}
			case "eof":
				p.Close()
			}
			deadline := time.Now().Add(time.Second)
			for {
				conn, err := net.DialTimeout("tcp", b.Endpoint.Address, 100*time.Millisecond)
				if err != nil {
					break
				}
				conn.Close()
				if time.Now().After(deadline) {
					t.Fatal("listener leaked after shutdown")
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
}

func TestBuiltinOwnerExitStillAcknowledgesDrain(t *testing.T) {
	p, _ := builtinTestPeer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := p.Handshake(ctx, []uint16{3}, "owner-exit"); err != nil {
		t.Fatal(err)
	}
	if err := p.Configure(ctx, builtinTestConfig()); err != nil {
		t.Fatal(err)
	}
	if err := p.DeliverCredential(ctx, nil); err != nil {
		t.Fatal(err)
	}
	b, err := p.WaitBound(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	child := ownerExitTestCommand(t)
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer child.Process.Kill()
	id, err := newChildIdentity(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.ChildStarted(ctx, id); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", b.Endpoint.Address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	child.Process.Kill()
	child.Wait()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = conn.Read(make([]byte, 1)); !errors.Is(err, io.EOF) && !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("owner exit did not close local connection: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	// Teardown is already complete, but the parent still owns the final DRAIN exchange.
	if err = p.Drain(ctx); err != nil {
		t.Fatalf("owner exit lost drain acknowledgement: %v", err)
	}
	if err = p.WaitStopped(ctx); err != nil {
		t.Fatal(err)
	}
}
