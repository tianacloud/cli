package supervisor

import (
	"context"
	"crypto/x509"
	"encoding/binary"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"

	"github.com/tianacloud/cli/internal/diagnostics"
	tiana "github.com/tianacloud/sdk-go"
)

// BuiltinHelperArgument is private process plumbing, not a user subcommand.
const BuiltinHelperArgument = "__tiana_builtin_helper"

// BuiltinHelperLauncher re-executes the CLI itself, never a PATH-resolved helper.
// Credentials still cross only the existing private framed control pipes.
type BuiltinHelperLauncher struct{}

func (BuiltinHelperLauncher) Launch(ctx context.Context, source CredentialSource) (HelperClient, error) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return nil, ErrHelperNotTrusted
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, ErrHelperNotTrusted
	}
	if runtime.GOOS == "linux" {
		executable = "/proc/self/exe"
	}
	return launchHelperCommand(ctx, exec.CommandContext(ctx, executable, BuiltinHelperArgument), source, nil)
}

type builtinFrame struct {
	kind    controlKind
	payload []byte
	err     error
}

func readBuiltinFrame(r io.Reader) builtinFrame {
	var length [4]byte
	if _, err := io.ReadFull(r, length[:]); err != nil {
		return builtinFrame{err: err}
	}
	n := binary.BigEndian.Uint32(length[:])
	if n < MinControlFrame || n > MaxControlFrame {
		return builtinFrame{err: ErrHelperProtocol}
	}
	frame := make([]byte, int(n)+4)
	copy(frame, length[:])
	if _, err := io.ReadFull(r, frame[4:]); err != nil {
		clear(frame)
		return builtinFrame{err: err}
	}
	k, p, err := parseControlFrame(frame)
	return builtinFrame{k, p, err}
}
func writeBuiltinFrame(w io.Writer, kind controlKind, payload []byte) error {
	if len(payload) > MaxControlPayload {
		return ErrHelperProtocol
	}
	var header [6]byte
	binary.BigEndian.PutUint32(header[:4], uint32(len(payload)+2))
	header[4] = ControlVersion
	header[5] = byte(kind)
	if err := writeAll(w, header[:]); err != nil {
		return err
	}
	return writeAll(w, payload)
}

// ServeBuiltinHelper runs the contract-3 server inside the private child process.
// Closing its parent control pipe or context always tears down the data plane.
func ServeBuiltinHelper(parent context.Context, in io.ReadCloser, out io.WriteCloser) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stopIO := context.AfterFunc(ctx, func() { in.Close(); out.Close() })
	defer stopIO()
	defer in.Close()
	frames := make(chan builtinFrame)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			f := readBuiltinFrame(in)
			select {
			case frames <- f:
			case <-ctx.Done():
				clear(f.payload)
				return
			}
			if f.err != nil {
				return
			}
		}
	}()
	defer func() { cancel(); in.Close(); <-readerDone }()
	read := func() (builtinFrame, error) {
		select {
		case f := <-frames:
			return f, f.err
		case <-ctx.Done():
			return builtinFrame{}, ctx.Err()
		}
	}
	require := func(kind controlKind) ([]byte, error) {
		f, e := read()
		if e != nil || f.kind != kind {
			clear(f.payload)
			return nil, ErrHelperProtocol
		}
		return f.payload, nil
	}
	reject := func() error { _ = writeBuiltinFrame(out, kindError, nil); return ErrHelperProtocol }
	p, err := require(kindHello)
	if err != nil {
		return reject()
	}
	d := wireDecoder{data: p}
	count, err := d.u8()
	supported := false
	if err != nil || count == 0 {
		return reject()
	}
	for i := 0; i < int(count); i++ {
		v, e := d.u16()
		if e != nil {
			return reject()
		}
		supported = supported || v == HelperContractVersion
	}
	size, e := d.u16()
	if e != nil || size == 0 || size > MaxControlString {
		return reject()
	}
	if _, e = d.take(int(size)); e != nil || d.remaining() != 0 || !supported {
		return reject()
	}
	var ack wireEncoder
	ack.u16(HelperContractVersion)
	ack.u64(CapabilitySQLDAdapter | CapabilitySQLDExplicitProfile)
	if err = writeBuiltinFrame(out, kindHelloAck, ack.bytes()); err != nil {
		return err
	}
	p, err = require(kindConfig)
	if err != nil {
		return reject()
	}
	config, err := decodeConfig(p)
	if err != nil || config.InsecureTLS {
		return reject()
	}
	// sdk-go verifies TLS unconditionally. Never downgrade when legacy config asks otherwise.
	var roots *x509.CertPool
	if config.UseWebPKIRoots {
		roots, err = x509.SystemCertPool()
		if err != nil {
			return reject()
		}
	} else {
		roots = x509.NewCertPool()
	}
	for _, der := range config.RootCertDER {
		cert, e := x509.ParseCertificate(der)
		if e != nil {
			return reject()
		}
		roots.AddCert(cert)
	}
	p, err = require(kindCredential)
	if err != nil {
		return reject()
	}
	token, err := builtinCredential(p, config.RequireToken)
	clear(p)
	if err != nil {
		return reject()
	}
	client, err := tiana.NewClient(tiana.Config{Endpoint: config.Endpoint, Token: token, RootCAs: roots, DialAddress: config.GatewayAddress, MaxStreams: builtinSessionLimit, OnRequestID: func(id string) { diagnostics.Write(ctx, "connect", id) }})
	if err != nil {
		return reject()
	}
	defer client.Close()
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return reject()
	}
	relay := newBuiltinRelay(ctx, listener, client, config.ExplicitProfile)
	defer relay.stop()
	scheme, _ := profileScheme(config.ExplicitProfile)
	var bound wireEncoder
	bound.string(scheme + "://" + listener.Addr().String())
	bound.u8(byte(SecurityLoopbackUnisolated))
	bound.u64(0)
	if err = writeBuiltinFrame(out, kindBound, bound.bytes()); err != nil {
		return err
	}
	if err = writeBuiltinFrame(out, kindReady, nil); err != nil {
		return err
	}
	f, err := read()
	if err != nil {
		return err
	}
	if (f.kind == kindDrain || f.kind == kindStop) && len(f.payload) == 0 {
		relay.stop()
		return writeBuiltinFrame(out, kindStopped, nil)
	}
	if f.kind != kindChildStarted {
		return reject()
	}
	identity, err := builtinChildIdentity(f.payload)
	if err != nil {
		return reject()
	}
	owner, err := watchBuiltinOwner(ctx, identity)
	if err != nil {
		return reject()
	}
	defer func() { cancel(); <-owner }()
	if err = writeBuiltinFrame(out, kindServing, nil); err != nil {
		return err
	}
	relay.start()
	select {
	case f = <-frames:
		if f.err != nil && f.err != io.EOF {
			return reject()
		}
		if f.err == nil && ((f.kind != kindDrain && f.kind != kindStop) || len(f.payload) != 0) {
			return reject()
		}
	case <-owner:
		// Stop the data plane immediately, but retain the control pipe until
		// the supervisor observes native exit and sends DRAIN. Exiting here
		// races its final write and can turn native success into EPIPE.
		relay.stop()
		f, err = read()
		if err != nil && err != io.EOF {
			return err
		}
		if err == nil && ((f.kind != kindDrain && f.kind != kindStop) || len(f.payload) != 0) {
			return reject()
		}
	case <-relay.failed:
	case <-ctx.Done():
		return ctx.Err()
	}
	relay.stop()
	if failure := relay.failure(); failure != nil {
		var message wireEncoder
		message.u8(byte(failure.Phase))
		message.u16(failure.Status)
		message.shortString(failure.Code)
		message.u32(failure.RetryAfterMS)
		if err = writeBuiltinFrame(out, kindSessionError, message.bytes()); err != nil {
			return err
		}
	}
	return writeBuiltinFrame(out, kindStopped, nil)
}

func builtinCredential(p []byte, required bool) (*tiana.Token, error) {
	d := wireDecoder{data: p}
	present, err := d.u8()
	if err != nil {
		return nil, ErrInvalidToken
	}
	if present == 0 && !required && d.remaining() == 0 {
		return nil, nil
	}
	if present != 1 {
		return nil, ErrInvalidToken
	}
	value, err := d.bytesField(MaxControlCredential)
	if err != nil || d.remaining() != 0 {
		return nil, ErrInvalidToken
	}
	return tiana.NewToken(string(value))
}
func builtinChildIdentity(p []byte) (ChildIdentity, error) {
	d := wireDecoder{data: p}
	pid, err := d.u32()
	if err != nil || pid == 0 || uint64(pid) > uint64(^uint(0)>>1) {
		return ChildIdentity{}, ErrHelperProtocol
	}
	start, err := d.bytesField(8)
	if err != nil || len(start) != 8 || d.remaining() != 0 {
		return ChildIdentity{}, ErrHelperProtocol
	}
	return ChildIdentity{PID: int(pid), StartTime: binary.BigEndian.Uint64(start)}, nil
}
