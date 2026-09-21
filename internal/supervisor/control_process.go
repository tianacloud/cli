package supervisor

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ProcessHelperLauncher is the production launcher for the frozen companion
// helper. Its stdin/stdout are private control streams and are never attached
// to the caller's terminal. No listener or data-plane descriptor is passed.
type ProcessHelperLauncher struct {
	Helper TrustedHelper
}

func (l ProcessHelperLauncher) Launch(ctx context.Context, source CredentialSource) (HelperClient, error) {
	if l.Helper.path == "" {
		return nil, ErrHelperNotTrusted
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(l.Helper.path)))
	if !trustedInstallChain(root) || !trustedRegular(l.Helper.path) {
		return nil, ErrHelperNotTrusted
	}
	manifest, err := readManifest(filepath.Join(root, "libexec", "tiana", trustedManifestName))
	if err != nil || manifest != l.Helper.manifest {
		return nil, ErrHelperNotTrusted
	}
	digest, err := fileDigest(l.Helper.path)
	if err != nil || !strings.EqualFold(hex.EncodeToString(digest[:]), manifest.SHA256) {
		return nil, ErrHelperNotTrusted
	}
	return launchHelperCommand(ctx, exec.CommandContext(contextOrBackground(ctx), l.Helper.path), source, nil)
}

func launchHelperCommand(
	ctx context.Context,
	command *exec.Cmd,
	source CredentialSource,
	cleanup func(),
) (HelperClient, error) {
	if cleanup == nil {
		cleanup = func() {}
	}
	if command == nil {
		cleanup()
		return nil, ErrHelperProtocol
	}
	toChildRead, toChildWrite, err := os.Pipe()
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("%w: create control pipe", ErrHelperProtocol)
	}
	fromChildRead, fromChildWrite, err := os.Pipe()
	if err != nil {
		cleanup()
		_ = toChildRead.Close()
		_ = toChildWrite.Close()
		return nil, fmt.Errorf("%w: create control pipe", ErrHelperProtocol)
	}
	command.Stdin = toChildRead
	command.Stdout = fromChildWrite
	command.Stderr = io.Discard
	command.Env = helperEnvironment(os.Environ(), source)
	configureHelperProcess(command)
	if err := command.Start(); err != nil {
		cleanup()
		_ = toChildRead.Close()
		_ = toChildWrite.Close()
		_ = fromChildRead.Close()
		_ = fromChildWrite.Close()
		return nil, fmt.Errorf("%w: start helper", ErrHelperProtocol)
	}
	_ = toChildRead.Close()
	_ = fromChildWrite.Close()
	done := make(chan error, 1)
	go func() {
		err := command.Wait()
		// On macOS the executable must remain available while the loader starts.
		// Release staged files (or the Linux memfd) only after the helper exits.
		cleanup()
		done <- err
	}()
	return &ProcessHelper{
		command: command,
		write:   toChildWrite,
		read:    fromChildRead,
		reader:  bufio.NewReader(fromChildRead),
		state:   StateStarting,
		done:    done,
	}, nil
}

type ProcessHelper struct {
	command *exec.Cmd
	write   io.WriteCloser
	read    io.ReadCloser
	reader  *bufio.Reader
	mu      sync.Mutex
	state   State
	closed  bool
	done    <-chan error
}

func (p *ProcessHelper) Handshake(ctx context.Context, versions []uint16, invocationID string) (HelperCapabilities, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.state != StateStarting {
		return HelperCapabilities{}, ErrHelperProtocol
	}
	if invocationID == "" || len(versions) == 0 {
		return HelperCapabilities{}, ErrHelperProtocol
	}
	p.state = StateHandshaking
	payload := encodeHello(versions, invocationID)
	if err := p.writeFrameContext(ctx, kindHello, payload); err != nil {
		return HelperCapabilities{}, err
	}
	kind, response, err := p.readFrameContext(ctx)
	if err != nil || kind != kindHelloAck {
		return HelperCapabilities{}, protocolResponseError(kind, err)
	}
	capabilities, err := decodeHelloAck(response)
	if err != nil || !containsVersion(versions, capabilities.SelectedVersion) {
		return HelperCapabilities{}, ErrHelperProtocol
	}
	return capabilities, nil
}

func (p *ProcessHelper) Configure(ctx context.Context, config HelperConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.state != StateHandshaking || validateHelperConfig(config) != nil {
		return ErrHelperProtocol
	}
	if err := p.writeFrameContext(ctx, kindConfig, encodeConfig(config)); err != nil {
		return err
	}
	return nil
}

func (p *ProcessHelper) WaitBound(ctx context.Context) (BoundInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.state != StateHandshaking {
		return BoundInfo{}, ErrHelperProtocol
	}
	kind, payload, err := p.readFrameContext(ctx)
	if err != nil || kind != kindBound {
		return BoundInfo{}, protocolResponseError(kind, err)
	}
	bound, err := decodeBound(payload)
	if err != nil {
		return BoundInfo{}, ErrHelperProtocol
	}
	p.state = StateBound
	return bound, nil
}

func (p *ProcessHelper) DeliverCredential(ctx context.Context, token *SecretToken) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.state != StateHandshaking {
		return ErrHelperProtocol
	}
	var bytes []byte
	if token != nil {
		bytes = token.BytesForHandoff()
	}
	payload := encodeCredential(bytes)
	defer zeroBytes(payload)
	if err := p.writeFrameContext(ctx, kindCredential, payload); err != nil {
		return err
	}
	return nil
}

func (p *ProcessHelper) WaitReady(ctx context.Context) (ReadyInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.state != StateBound {
		return ReadyInfo{}, ErrHelperProtocol
	}
	kind, payload, err := p.readFrameContext(ctx)
	if err != nil || kind != kindReady {
		return ReadyInfo{}, protocolResponseError(kind, err)
	}
	if len(payload) != 0 {
		return ReadyInfo{}, ErrHelperProtocol
	}
	p.state = StateReady
	return ReadyInfo{}, nil
}

func (p *ProcessHelper) ChildStarted(ctx context.Context, identity ChildIdentity) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.state != StateReady || identity.PID <= 0 {
		return ErrHelperProtocol
	}
	if err := p.writeFrameContext(ctx, kindChildStarted, encodeChildStarted(identity)); err != nil {
		return err
	}
	kind, payload, err := p.readFrameContext(ctx)
	if err != nil || kind != kindServing || len(payload) != 0 {
		return protocolResponseError(kind, err)
	}
	p.state = StateServing
	return nil
}

func (p *ProcessHelper) Drain(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.state == StateStopped {
		return nil
	}
	if p.state == StateDraining {
		return nil
	}
	if p.state != StateServing && p.state != StateReady && p.state != StateBound && p.state != StateHandshaking {
		return ErrHelperProtocol
	}
	if err := p.writeFrameContext(ctx, kindDrain, nil); err != nil {
		return err
	}
	p.state = StateDraining
	return nil
}

func (p *ProcessHelper) WaitStopped(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.state == StateStopped {
		return nil
	}
	if p.state != StateDraining {
		return ErrHelperProtocol
	}
	var sessionErr error
	for frames := 0; frames < 2; frames++ {
		kind, payload, err := p.readFrameContext(ctx)
		if err != nil {
			return protocolResponseError(kind, err)
		}
		switch kind {
		case kindSessionError:
			if sessionErr != nil {
				return ErrHelperProtocol
			}
			failure, decodeErr := decodeSessionError(payload)
			if decodeErr != nil {
				return decodeErr
			}
			sessionErr = failure
		case kindStopped:
			if len(payload) != 0 {
				return ErrHelperProtocol
			}
			p.state = StateStopped
			return sessionErr
		default:
			return protocolResponseError(kind, nil)
		}
	}
	return ErrHelperProtocol
}

func (p *ProcessHelper) Close() error {
	p.mu.Lock()
	if p.closed && p.write == nil && p.read == nil && p.command == nil {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	write, read, command, done := p.write, p.read, p.command, p.done
	p.write, p.read, p.command, p.done = nil, nil, nil, nil
	p.mu.Unlock()
	if write != nil {
		_ = write.Close()
	}
	if read != nil {
		_ = read.Close()
	}
	if command != nil && command.Process != nil {
		_ = command.Process.Kill()
		if done != nil {
			select {
			case <-done:
			case <-time.After(time.Second):
			}
		}
	}
	return nil
}

func (p *ProcessHelper) writeFrame(kind controlKind, payload []byte) error {
	return p.writeFrameContext(context.Background(), kind, payload)
}

func (p *ProcessHelper) writeFrameContext(ctx context.Context, kind controlKind, payload []byte) error {
	if kind == 0 || !knownControlKind(kind) || len(payload) > MaxControlPayload {
		return ErrHelperProtocol
	}
	frameLen := 2 + len(payload)
	if frameLen < MinControlFrame || frameLen > MaxControlFrame {
		return ErrHelperProtocol
	}
	frame := make([]byte, 4+frameLen)
	if kind == kindCredential {
		defer zeroBytes(frame)
	}
	binary.BigEndian.PutUint32(frame[:4], uint32(frameLen))
	frame[4] = ControlVersion
	frame[5] = byte(kind)
	copy(frame[6:], payload)
	if p.write == nil {
		return ErrHelperProtocol
	}
	write := p.write
	done := make(chan error, 1)
	go func() { done <- writeAll(write, frame) }()
	operationContext := contextOrBackground(ctx)
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("%w: write control frame", ErrHelperProtocol)
		}
		return nil
	case <-operationContext.Done():
		p.abortIO()
		// Closing the OS pipe interrupts the writer. Join before the deferred
		// frame wipe so no goroutine can still read the credential bytes.
		<-done
		return operationContext.Err()
	}
}

func (p *ProcessHelper) readFrame() (controlKind, []byte, error) {
	return p.readFrameContext(context.Background())
}

func (p *ProcessHelper) readFrameContext(ctx context.Context) (controlKind, []byte, error) {
	if p.reader == nil || p.read == nil {
		return 0, nil, ErrHelperProtocol
	}
	reader := p.reader
	type result struct {
		kind    controlKind
		payload []byte
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		var lengthBytes [4]byte
		if _, err := io.ReadFull(reader, lengthBytes[:]); err != nil {
			resultCh <- result{err: err}
			return
		}
		length := binary.BigEndian.Uint32(lengthBytes[:])
		if length < MinControlFrame || length > MaxControlFrame {
			resultCh <- result{err: ErrHelperProtocol}
			return
		}
		frame := make([]byte, length)
		if _, err := io.ReadFull(reader, frame); err != nil {
			resultCh <- result{err: err}
			return
		}
		fullFrame := make([]byte, 4+len(frame))
		binary.BigEndian.PutUint32(fullFrame[:4], length)
		copy(fullFrame[4:], frame)
		kind, payload, err := parseControlFrame(fullFrame)
		if err != nil {
			resultCh <- result{err: err}
			return
		}
		resultCh <- result{kind: kind, payload: payload}
	}()
	operationContext := contextOrBackground(ctx)
	select {
	case result := <-resultCh:
		return result.kind, result.payload, result.err
	case <-operationContext.Done():
		p.abortIO()
		// Closing the read end interrupts io.ReadFull. Join so the read
		// goroutine cannot outlive this operation or retain its frame.
		<-resultCh
		return 0, nil, operationContext.Err()
	}
}

func writeAll(writer io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := writer.Write(value)
		if written < 0 || written > len(value) {
			return io.ErrShortWrite
		}
		if written > 0 {
			value = value[written:]
		}
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (p *ProcessHelper) abortIO() {
	p.closed = true
	if p.write != nil {
		_ = p.write.Close()
	}
	if p.read != nil {
		_ = p.read.Close()
	}
}

func (p *ProcessHelper) Done() <-chan error { return p.done }

// parseControlFrame validates a complete private control frame before any
// message-specific decoding or side effect. Keeping this check in one
// production helper also lets the checked-in cross-language rejection vectors
// exercise the exact framing boundary used by ProcessHelper.
func parseControlFrame(frame []byte) (controlKind, []byte, error) {
	if len(frame) < 4 {
		return 0, nil, ErrHelperProtocol
	}
	length := binary.BigEndian.Uint32(frame[:4])
	if length < MinControlFrame || length > MaxControlFrame || int(length) != len(frame)-4 {
		return 0, nil, ErrHelperProtocol
	}
	body := frame[4:]
	if body[0] != ControlVersion || !knownControlKind(controlKind(body[1])) {
		return 0, nil, ErrHelperProtocol
	}
	return controlKind(body[1]), body[2:], nil
}

func knownControlKind(kind controlKind) bool { return kind >= kindHello && kind <= kindSessionError }

func decodeSessionError(payload []byte) (SessionFailureError, error) {
	decoder := wireDecoder{data: payload}
	phase, err := decoder.u8()
	if err != nil {
		return SessionFailureError{}, ErrHelperProtocol
	}
	status, err := decoder.u16()
	if err != nil {
		return SessionFailureError{}, ErrHelperProtocol
	}
	codeLength, err := decoder.u16()
	if err != nil || codeLength == 0 || codeLength > 64 {
		return SessionFailureError{}, ErrHelperProtocol
	}
	codeBytes, err := decoder.take(int(codeLength))
	if err != nil {
		return SessionFailureError{}, ErrHelperProtocol
	}
	retryAfterMS, err := decoder.u32()
	if err != nil || decoder.remaining() != 0 {
		return SessionFailureError{}, ErrHelperProtocol
	}
	failure := SessionFailureError{
		Phase:        SessionFailurePhase(phase),
		Status:       status,
		Code:         string(codeBytes),
		RetryAfterMS: retryAfterMS,
	}
	if !validSessionFailureCode(failure.Code) || failure.RetryAfterMS > 60_000 {
		return SessionFailureError{}, ErrHelperProtocol
	}
	if failure.Status != 0 && (failure.Status < 400 || failure.Status > 599) {
		return SessionFailureError{}, ErrHelperProtocol
	}
	switch failure.Phase {
	case SessionFailureLocalRequest:
		if failure.Status != 0 || failure.RetryAfterMS != 0 {
			return SessionFailureError{}, ErrHelperProtocol
		}
	case SessionFailureBeforeConnect:
	case SessionFailureAfterConnect:
		if failure.Status != 0 || failure.RetryAfterMS != 0 {
			return SessionFailureError{}, ErrHelperProtocol
		}
	default:
		return SessionFailureError{}, ErrHelperProtocol
	}
	return failure, nil
}

func protocolResponseError(kind controlKind, err error) error {
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return fmt.Errorf("%w: response", ErrHelperProtocol)
	}
	if kind == kindError {
		return fmt.Errorf("%w: helper rejected request", ErrHelperProtocol)
	}
	return ErrHelperProtocol
}

func containsVersion(versions []uint16, selected uint16) bool {
	for _, version := range versions {
		if version == selected {
			return true
		}
	}
	return false
}

func encodeHello(versions []uint16, invocationID string) []byte {
	var encoder wireEncoder
	encoder.u8(uint8(len(versions)))
	for _, version := range versions {
		encoder.u16(version)
	}
	encoder.shortString(invocationID)
	return encoder.bytes()
}

func encodeConfig(config HelperConfig) []byte {
	var encoder wireEncoder
	encoder.string(config.Endpoint)
	encoder.string(config.AdapterID)
	encoder.u8(uint8(len(config.AllowedProfile)))
	for _, profile := range config.AllowedProfile {
		encoder.u8(profileCode(profile))
	}
	encoder.u8(selectionModeCode(config.SelectionMode))
	if config.ExplicitProfile == "" {
		encoder.u8(0)
	} else {
		encoder.u8(profileCode(config.ExplicitProfile))
	}
	encoder.u8(byte(SecurityLoopbackUnisolated))
	if config.RequireToken {
		encoder.u8(1)
	} else {
		encoder.u8(0)
	}
	encoder.u32(uint32(config.Deadline.Milliseconds()))
	encoder.u32(uint32(config.HeaderLimit))
	encoder.u32(uint32(config.FieldLimit))
	encoder.u32(uint32(config.Pre200Limit))
	encoder.optionalString(config.GatewayAddress)
	if config.UseWebPKIRoots {
		encoder.u8(1)
	} else {
		encoder.u8(0)
	}
	encoder.u8(uint8(len(config.RootCertDER)))
	for _, certificate := range config.RootCertDER {
		encoder.bytesField(certificate)
	}
	if config.InsecureTLS {
		encoder.u8(1)
	} else {
		encoder.u8(0)
	}
	return encoder.bytes()
}

func encodeCredential(value []byte) []byte {
	var encoder wireEncoder
	if len(value) == 0 {
		encoder.u8(0)
		return encoder.bytes()
	}
	encoder.u8(1)
	encoder.bytesField(value)
	return encoder.bytes()
}

func encodeChildStarted(identity ChildIdentity) []byte {
	var encoder wireEncoder
	encoder.u32(uint32(identity.PID))
	var startTime [8]byte
	binary.BigEndian.PutUint64(startTime[:], identity.StartTime)
	encoder.bytesField(startTime[:])
	return encoder.bytes()
}

type wireEncoder struct{ data []byte }

func (e *wireEncoder) u8(value byte) { e.data = append(e.data, value) }
func (e *wireEncoder) u16(value uint16) {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], value)
	e.data = append(e.data, b[:]...)
}
func (e *wireEncoder) u32(value uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], value)
	e.data = append(e.data, b[:]...)
}
func (e *wireEncoder) u64(value uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], value)
	e.data = append(e.data, b[:]...)
}
func (e *wireEncoder) raw(value []byte)         { e.data = append(e.data, value...) }
func (e *wireEncoder) string(value string)      { e.u32(uint32(len(value))); e.raw([]byte(value)) }
func (e *wireEncoder) shortString(value string) { e.u16(uint16(len(value))); e.raw([]byte(value)) }
func (e *wireEncoder) optionalString(value string) {
	if value == "" {
		e.u8(0)
		return
	}
	e.u8(1)
	e.string(value)
}
func (e *wireEncoder) bytesField(value []byte) { e.u32(uint32(len(value))); e.raw(value) }
func (e *wireEncoder) bytes() []byte           { return e.data }

type wireDecoder struct {
	data   []byte
	offset int
}

func (d *wireDecoder) take(count int) ([]byte, error) {
	if count < 0 || d.offset+count > len(d.data) {
		return nil, ErrHelperProtocol
	}
	value := d.data[d.offset : d.offset+count]
	d.offset += count
	return value, nil
}
func (d *wireDecoder) u8() (byte, error) {
	value, err := d.take(1)
	if err != nil {
		return 0, err
	}
	return value[0], nil
}
func (d *wireDecoder) u16() (uint16, error) {
	value, err := d.take(2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(value), nil
}
func (d *wireDecoder) u32() (uint32, error) {
	value, err := d.take(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(value), nil
}
func (d *wireDecoder) u64() (uint64, error) {
	value, err := d.take(8)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(value), nil
}
func (d *wireDecoder) string() (string, error) {
	length, err := d.u32()
	if err != nil || length > MaxControlString {
		return "", ErrHelperProtocol
	}
	value, err := d.take(int(length))
	if err != nil {
		return "", err
	}
	return string(value), nil
}
func (d *wireDecoder) optionalString() (string, error) {
	present, err := d.u8()
	if err != nil {
		return "", err
	}
	switch present {
	case 0:
		return "", nil
	case 1:
		return d.string()
	default:
		return "", ErrHelperProtocol
	}
}
func (d *wireDecoder) bytesField(limit int) ([]byte, error) {
	length, err := d.u32()
	if err != nil || length == 0 || uint64(length) > uint64(limit) {
		return nil, ErrHelperProtocol
	}
	return d.take(int(length))
}
func (d *wireDecoder) remaining() int { return len(d.data) - d.offset }

func decodeHelloAck(payload []byte) (HelperCapabilities, error) {
	decoder := wireDecoder{data: payload}
	selected, err := decoder.u16()
	if err != nil {
		return HelperCapabilities{}, err
	}
	bits, err := decoder.u64()
	if err != nil || decoder.remaining() != 0 {
		return HelperCapabilities{}, ErrHelperProtocol
	}
	return HelperCapabilities{
		SelectedVersion:     selected,
		RawBits:             bits,
		SQLDAdapter:         bits&CapabilitySQLDAdapter != 0,
		SQLDExplicitProfile: bits&CapabilitySQLDExplicitProfile != 0,
	}, nil
}

func decodeConfig(payload []byte) (HelperConfig, error) {
	decoder := wireDecoder{data: payload}
	endpoint, err := decoder.string()
	if err != nil {
		return HelperConfig{}, err
	}
	adapterID, err := decoder.string()
	if err != nil {
		return HelperConfig{}, err
	}
	profileCount, err := decoder.u8()
	if err != nil {
		return HelperConfig{}, err
	}
	profiles := make([]string, 0, int(profileCount))
	for i := 0; i < int(profileCount); i++ {
		code, err := decoder.u8()
		if err != nil {
			return HelperConfig{}, err
		}
		profile, ok := profileName(code)
		if !ok {
			return HelperConfig{}, ErrHelperProtocol
		}
		profiles = append(profiles, profile)
	}
	selection, err := decoder.u8()
	if err != nil {
		return HelperConfig{}, err
	}
	selectionMode, ok := selectionModeName(selection)
	if !ok {
		return HelperConfig{}, ErrHelperProtocol
	}
	explicit, err := decoder.u8()
	if err != nil {
		return HelperConfig{}, err
	}
	var explicitProfile string
	if explicit != 0 {
		profile, ok := profileName(explicit)
		if !ok {
			return HelperConfig{}, ErrHelperProtocol
		}
		explicitProfile = profile
	}
	security, err := decoder.u8()
	if err != nil || security != byte(SecurityLoopbackUnisolated) {
		return HelperConfig{}, ErrHelperProtocol
	}
	requireToken, err := decoder.u8()
	if err != nil || requireToken > 1 {
		return HelperConfig{}, ErrHelperProtocol
	}
	deadlineMS, err := decoder.u32()
	if err != nil {
		return HelperConfig{}, err
	}
	headerLimit, err := decoder.u32()
	if err != nil {
		return HelperConfig{}, err
	}
	fieldLimit, err := decoder.u32()
	if err != nil {
		return HelperConfig{}, err
	}
	pre200Limit, err := decoder.u32()
	if err != nil {
		return HelperConfig{}, err
	}
	gatewayAddress, err := decoder.optionalString()
	if err != nil {
		return HelperConfig{}, ErrHelperProtocol
	}
	useWebPKI, err := decoder.u8()
	if err != nil || useWebPKI > 1 {
		return HelperConfig{}, ErrHelperProtocol
	}
	rootCount, err := decoder.u8()
	if err != nil || rootCount > GatewayRootCertCount {
		return HelperConfig{}, ErrHelperProtocol
	}
	var rootCertificates [][]byte
	if rootCount > 0 {
		rootCertificates = make([][]byte, 0, int(rootCount))
	}
	for i := 0; i < int(rootCount); i++ {
		certificate, err := decoder.bytesField(GatewayRootCertLimit)
		if err != nil {
			return HelperConfig{}, ErrHelperProtocol
		}
		rootCertificates = append(rootCertificates, append([]byte(nil), certificate...))
	}
	insecureTLS, err := decoder.u8()
	if err != nil || insecureTLS > 1 {
		return HelperConfig{}, ErrHelperProtocol
	}
	if decoder.remaining() != 0 {
		return HelperConfig{}, ErrHelperProtocol
	}
	config := HelperConfig{
		Endpoint:        endpoint,
		AdapterID:       adapterID,
		AllowedProfile:  profiles,
		SelectionMode:   selectionMode,
		ExplicitProfile: explicitProfile,
		Deadline:        time.Duration(deadlineMS) * time.Millisecond,
		HeaderLimit:     int(headerLimit),
		FieldLimit:      int(fieldLimit),
		Pre200Limit:     int(pre200Limit),
		RequireToken:    requireToken == 1,
		GatewayAddress:  gatewayAddress,
		UseWebPKIRoots:  useWebPKI == 1,
		InsecureTLS:     insecureTLS == 1,
		RootCertDER:     rootCertificates,
	}
	if err := validateHelperConfig(config); err != nil {
		return HelperConfig{}, err
	}
	return config, nil
}

func decodeBound(payload []byte) (BoundInfo, error) {
	decoder := wireDecoder{data: payload}
	locator, err := decoder.string()
	if err != nil {
		return BoundInfo{}, err
	}
	security, err := decoder.u8()
	if err != nil || security > byte(SecurityStrictProcess) {
		return BoundInfo{}, ErrHelperProtocol
	}
	capability, err := decoder.u64()
	if err != nil || decoder.remaining() != 0 {
		return BoundInfo{}, ErrHelperProtocol
	}
	endpoint, err := localEndpointFromLocator(locator, SecurityLevel(security), capability)
	if err != nil {
		return BoundInfo{}, err
	}
	return BoundInfo{Endpoint: endpoint}, nil
}

func profileCode(value string) byte {
	switch value {
	case "hrana-http":
		return 1
	case "hrana-websocket":
		return 2
	default:
		return 0
	}
}

func profileName(value byte) (string, bool) {
	switch value {
	case 1:
		return "hrana-http", true
	case 2:
		return "hrana-websocket", true
	default:
		return "", false
	}
}

func validateHelperConfig(config HelperConfig) error {
	if config.Endpoint == "" || config.AdapterID != SQLDAdapterID {
		return ErrHelperProtocol
	}
	if config.Deadline != SQLDClassifierDeadline || config.HeaderLimit != SQLDHeaderLimit || config.FieldLimit != SQLDFieldLimit || config.Pre200Limit != SQLDPre200Limit {
		return ErrHelperProtocol
	}
	if err := validateGatewayAddress(config.GatewayAddress); err != nil {
		return ErrHelperProtocol
	}
	if len(config.RootCertDER) > GatewayRootCertCount || config.InsecureTLS && (config.UseWebPKIRoots || len(config.RootCertDER) != 0) || !config.InsecureTLS && !config.UseWebPKIRoots && len(config.RootCertDER) == 0 {
		return ErrHelperProtocol
	}
	payloadSize := 128 + len(config.Endpoint) + len(config.AdapterID) + len(config.GatewayAddress)
	for _, certificate := range config.RootCertDER {
		if len(certificate) == 0 || len(certificate) > GatewayRootCertLimit {
			return ErrHelperProtocol
		}
		payloadSize += 4 + len(certificate)
	}
	if payloadSize > MaxControlPayload {
		return ErrHelperProtocol
	}
	seen := [3]bool{}
	for _, profile := range config.AllowedProfile {
		code := profileCode(profile)
		if code == 0 || seen[code] {
			return ErrHelperProtocol
		}
		seen[code] = true
	}
	switch config.SelectionMode {
	case SelectionModeBoundedHTTPHeaderClassifier:
		if config.ExplicitProfile != "" || len(config.AllowedProfile) != 2 || !seen[1] || !seen[2] {
			return ErrHelperProtocol
		}
	case SelectionModeExplicitProfile:
		if !validSQLDProfile(config.ExplicitProfile) || len(config.AllowedProfile) != 2 || !seen[1] || !seen[2] {
			return ErrHelperProtocol
		}
		matched := false
		for _, profile := range config.AllowedProfile {
			matched = matched || profile == config.ExplicitProfile
		}
		if !matched {
			return ErrHelperProtocol
		}
	default:
		return ErrHelperProtocol
	}
	return nil
}

func selectionModeCode(value string) byte {
	switch value {
	case SelectionModeBoundedHTTPHeaderClassifier:
		return 1
	case SelectionModeExplicitProfile:
		return 2
	}
	return 0
}

func selectionModeName(value byte) (string, bool) {
	switch value {
	case 1:
		return SelectionModeBoundedHTTPHeaderClassifier, true
	case 2:
		return SelectionModeExplicitProfile, true
	}
	return "", false
}

func localEndpointFromLocator(locator string, security SecurityLevel, capability uint64) (LocalEndpoint, error) {
	parsed, err := neturlParse(locator)
	if err != nil {
		return LocalEndpoint{}, ErrHelperProtocol
	}
	if parsed.scheme == "http" || parsed.scheme == "ws" {
		if !isLoopbackHost(parsed.host) || parsed.port == "" {
			return LocalEndpoint{}, ErrHelperProtocol
		}
		return LocalEndpoint{Network: "tcp", Address: parsed.host + ":" + parsed.port, URL: locator, SecurityLevel: security, Capability: capability}, nil
	}
	if parsed.scheme == "unix" && parsed.path != "" {
		return LocalEndpoint{Network: "unix", Address: parsed.path, URL: locator, SecurityLevel: security, Capability: capability}, nil
	}
	return LocalEndpoint{}, ErrHelperProtocol
}

type parsedLocator struct{ scheme, host, port, path string }

func neturlParse(value string) (parsedLocator, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return parsedLocator{}, ErrHelperProtocol
	}
	return parsedLocator{scheme: parsed.Scheme, host: parsed.Hostname(), port: parsed.Port(), path: parsed.Path}, nil
}

func zeroBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

var _ HelperClient = (*ProcessHelper)(nil)
var _ HelperLauncher = ProcessHelperLauncher{}
