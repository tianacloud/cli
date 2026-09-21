package supervisor

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const (
	controlArtifactSHA256 = "d9f08c9d7a1154d2c27552786602083b71f53fb92adf1255b5754f7dfa5dcaf3"
	controlArtifactDigest = "10ce70dd3c52e0bc"
)

type artifactFrame struct {
	label string
	frame []byte
}

type controlArtifact struct {
	raw        string
	metadata   map[string]string
	vectors    []artifactFrame
	rejections []artifactFrame
}

func TestCheckedInControlArtifactMatchesGoEncoder(t *testing.T) {
	artifact := readControlArtifact(t)
	assertArtifactMetadata(t, artifact)

	wantVectors := canonicalGoVectors()
	if len(artifact.vectors) != len(wantVectors) {
		t.Fatalf("vector count=%d, want %d", len(artifact.vectors), len(wantVectors))
	}
	for i, want := range wantVectors {
		got := artifact.vectors[i]
		if got.label != want.label {
			t.Fatalf("vector[%d] label=%q, want %q", i, got.label, want.label)
		}
		if !bytes.Equal(got.frame, want.frame) {
			t.Fatalf("vector %s differs from the Go canonical encoder\n got: %x\nwant: %x", got.label, got.frame, want.frame)
		}
		if _, _, err := parseControlFrame(got.frame); err != nil {
			t.Fatalf("vector %s does not pass production frame validation: %v", got.label, err)
		}
		roundTripped, err := roundTripArtifactFrame(t, got)
		if err != nil {
			t.Fatalf("vector %s decode: %v", got.label, err)
		}
		if !bytes.Equal(roundTripped, got.frame) {
			t.Fatalf("vector %s is not stable under Go decode→encode\n got: %x\nwant: %x", got.label, roundTripped, got.frame)
		}
	}

	if got := fnv1a64(artifact.vectors); got != artifact.metadata["digest"] {
		t.Fatalf("artifact digest=%s, computed=%s", artifact.metadata["digest"], got)
	}
	if got := fnv1a64(artifact.vectors); got != controlArtifactDigest {
		t.Fatalf("artifact digest=%s, want pinned digest %s", got, controlArtifactDigest)
	}

	assertCanonicalConfig(t, artifact.vectors)
	assertCanonicalChildStarted(t, artifact.vectors)
	assertArtifactRejections(t, artifact.rejections)
}

func readControlArtifact(t *testing.T) controlArtifact {
	t.Helper()
	path := os.Getenv("TIANA_CONTROL_ARTIFACT")
	if path == "" {
		_, source, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("runtime.Caller failed while locating the checked-in public artifact")
		}
		path = filepath.Join(filepath.Dir(source), "testdata", "control_vectors.txt")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read public control artifact %s: %v", path, err)
	}
	raw := string(contents)
	if sum := sha256.Sum256(contents); hex.EncodeToString(sum[:]) != controlArtifactSHA256 {
		t.Fatalf("Public control artifact sha256=%x, want %s", sum, controlArtifactSHA256)
	}

	artifact := controlArtifact{
		raw:      raw,
		metadata: make(map[string]string),
	}
	section := ""
	lines := strings.Split(raw, "\n")
	for lineNumber, line := range lines {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			if section != "vectors" && section != "rejections" {
				t.Fatalf("line %d: unknown artifact section %q", lineNumber+1, section)
			}
			continue
		}
		label, value, ok := strings.Cut(line, "=")
		if !ok || label == "" {
			t.Fatalf("line %d: expected label=value, got %q", lineNumber+1, line)
		}
		if section == "" {
			if _, exists := artifact.metadata[label]; exists {
				t.Fatalf("line %d: duplicate metadata key %q", lineNumber+1, label)
			}
			artifact.metadata[label] = value
			continue
		}
		frame, err := hex.DecodeString(value)
		if err != nil {
			t.Fatalf("line %d: %s=%q is not hex: %v", lineNumber+1, label, value, err)
		}
		entry := artifactFrame{label: label, frame: frame}
		entries := &artifact.vectors
		if section == "rejections" {
			entries = &artifact.rejections
		}
		for _, existing := range *entries {
			if existing.label == label {
				t.Fatalf("line %d: duplicate %s label %q", lineNumber+1, section, label)
			}
		}
		*entries = append(*entries, entry)
	}
	return artifact
}

func assertArtifactMetadata(t *testing.T, artifact controlArtifact) {
	t.Helper()
	want := map[string]string{
		"artifact_version":        "5",
		"frame_length_prefix":     "u32-be",
		"frame_body":              "version:u8,kind:u8,payload",
		"control_version":         "1",
		"helper_contract_version": "3",
		"digest_algorithm":        "fnv1a64",
		"digest":                  controlArtifactDigest,
		"config_tail":             "require_token:u8,deadline_ms:u32,header_limit_bytes:u32,field_limit:u32,pre200_limit_bytes:u32,gateway_address_present:u8,gateway_address:text_if_present,use_webpki_roots:u8,root_certificate_count:u8,root_certificate:bytes_repeated,insecure_tls:u8",
		"config_values":           "deadline_ms:1000,header_limit_bytes:16384,field_limit:64,pre200_limit_bytes:65536,gateway_address_present:0,use_webpki_roots:1,root_certificate_count:0,insecure_tls:0",
	}
	for key, expected := range want {
		if actual := artifact.metadata[key]; actual != expected {
			t.Fatalf("artifact metadata %s=%q, want %q", key, actual, expected)
		}
	}
	if len(artifact.metadata) != len(want) {
		t.Fatalf("artifact metadata keys=%d, want exactly %d", len(artifact.metadata), len(want))
	}

	tail := strings.Split(artifact.metadata["config_tail"], ",")
	wantTail := []struct{ name, width string }{
		{"require_token", "u8"},
		{"deadline_ms", "u32"},
		{"header_limit_bytes", "u32"},
		{"field_limit", "u32"},
		{"pre200_limit_bytes", "u32"},
		{"gateway_address_present", "u8"},
		{"gateway_address", "text_if_present"},
		{"use_webpki_roots", "u8"},
		{"root_certificate_count", "u8"},
		{"root_certificate", "bytes_repeated"},
		{"insecure_tls", "u8"},
	}
	if len(tail) != len(wantTail) {
		t.Fatalf("CONFIG field count=%d, want %d", len(tail), len(wantTail))
	}
	for i, field := range tail {
		name, width, ok := strings.Cut(field, ":")
		if !ok || name != wantTail[i].name || width != wantTail[i].width {
			t.Fatalf("CONFIG field[%d]=%q, want %s:%s", i, field, wantTail[i].name, wantTail[i].width)
		}
	}

	values := parseArtifactValues(t, artifact.metadata["config_values"])
	wantValues := map[string]uint64{
		"deadline_ms":             1000,
		"header_limit_bytes":      SQLDHeaderLimit,
		"field_limit":             SQLDFieldLimit,
		"pre200_limit_bytes":      SQLDPre200Limit,
		"gateway_address_present": 0,
		"use_webpki_roots":        1,
		"root_certificate_count":  0,
		"insecure_tls":            0,
	}
	for key, expected := range wantValues {
		if actual := values[key]; actual != expected {
			t.Fatalf("CONFIG value %s=%d, want %d", key, actual, expected)
		}
	}
	if len(values) != len(wantValues) {
		t.Fatalf("CONFIG value keys=%d, want exactly %d", len(values), len(wantValues))
	}
}

func parseArtifactValues(t *testing.T, encoded string) map[string]uint64 {
	t.Helper()
	values := make(map[string]uint64)
	for _, field := range strings.Split(encoded, ",") {
		name, value, ok := strings.Cut(field, ":")
		if !ok || name == "" {
			t.Fatalf("invalid CONFIG value %q", field)
		}
		if _, exists := values[name]; exists {
			t.Fatalf("duplicate CONFIG value %q", name)
		}
		parsed, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			t.Fatalf("CONFIG value %s=%q: %v", name, value, err)
		}
		values[name] = parsed
	}
	return values
}

func canonicalGoVectors() []artifactFrame {
	config := HelperConfig{
		Endpoint:        "ep-01j5c9m7q2v8x4k6n3r0t1w2yz.db.example.test",
		AdapterID:       SQLDAdapterID,
		AllowedProfile:  []string{"hrana-http", "hrana-websocket"},
		SelectionMode:   "bounded-http-header-classifier",
		ExplicitProfile: "",
		Deadline:        SQLDClassifierDeadline,
		HeaderLimit:     SQLDHeaderLimit,
		FieldLimit:      SQLDFieldLimit,
		Pre200Limit:     SQLDPre200Limit,
		RequireToken:    true,
		UseWebPKIRoots:  true,
	}
	explicitHTTP := config
	explicitHTTP.SelectionMode = SelectionModeExplicitProfile
	explicitHTTP.ExplicitProfile = ProfileHranaHTTP
	explicitWS := config
	explicitWS.SelectionMode = SelectionModeExplicitProfile
	explicitWS.ExplicitProfile = ProfileHranaWebSocket
	return []artifactFrame{
		{label: "HELLO", frame: canonicalFrame(kindHello, encodeHello([]uint16{HelperContractVersion}, "invocation-0001"))},
		{label: "HELLO_ACK", frame: canonicalFrame(kindHelloAck, canonicalHelloAckPayload(HelperContractVersion, 15))},
		{label: "CONFIG_CLASSIFIER", frame: canonicalFrame(kindConfig, encodeConfig(config))},
		{label: "CONFIG_EXPLICIT_HTTP", frame: canonicalFrame(kindConfig, encodeConfig(explicitHTTP))},
		{label: "CONFIG_EXPLICIT_WS", frame: canonicalFrame(kindConfig, encodeConfig(explicitWS))},
		{label: "CREDENTIAL_ABSENT", frame: canonicalFrame(kindCredential, encodeCredential(nil))},
		{label: "CREDENTIAL_PRESENT", frame: canonicalFrame(kindCredential, encodeCredential([]byte("credential-vector-canary")))},
		{label: "BOUND", frame: canonicalFrame(kindBound, canonicalBoundPayload())},
		{label: "BOUND_WS", frame: canonicalFrame(kindBound, canonicalBoundPayloadFor("ws://127.0.0.1:43127"))},
		{label: "READY", frame: canonicalFrame(kindReady, nil)},
		{label: "CHILD_STARTED", frame: canonicalFrame(kindChildStarted, encodeChildStarted(ChildIdentity{PID: 4242, StartTime: 0x0102030405060708}))},
		{label: "SERVING", frame: canonicalFrame(kindServing, nil)},
		{label: "DRAIN", frame: canonicalFrame(kindDrain, nil)},
		{label: "STOP", frame: canonicalFrame(kindStop, nil)},
		{label: "STOPPED", frame: canonicalFrame(kindStopped, nil)},
		{label: "ERROR", frame: canonicalFrame(kindError, canonicalErrorPayload(1))},
		{label: "SESSION_ERROR", frame: canonicalFrame(kindSessionError, canonicalSessionErrorPayload(SessionFailureError{
			Phase:  SessionFailureBeforeConnect,
			Status: 407,
			Code:   "AUTH_REQUIRED",
		}))},
	}
}

func canonicalFrame(kind controlKind, payload []byte) []byte {
	frameLength := 2 + len(payload)
	frame := make([]byte, 4+frameLength)
	binary.BigEndian.PutUint32(frame[:4], uint32(frameLength))
	frame[4] = ControlVersion
	frame[5] = byte(kind)
	copy(frame[6:], payload)
	return frame
}

func canonicalHelloAckPayload(selected uint16, capabilities uint64) []byte {
	var encoder wireEncoder
	encoder.u16(selected)
	encoder.u64(capabilities)
	return encoder.bytes()
}

func canonicalBoundPayload() []byte {
	return canonicalBoundPayloadFor("http://127.0.0.1:43127")
}

func canonicalBoundPayloadFor(locator string) []byte {
	var encoder wireEncoder
	encoder.string(locator)
	encoder.u8(byte(SecurityLoopbackUnisolated))
	encoder.u64(0x1122334455667788)
	return encoder.bytes()
}

func canonicalErrorPayload(code uint16) []byte {
	var encoder wireEncoder
	encoder.u16(code)
	return encoder.bytes()
}

func canonicalSessionErrorPayload(failure SessionFailureError) []byte {
	var encoder wireEncoder
	encoder.u8(byte(failure.Phase))
	encoder.u16(failure.Status)
	encoder.shortString(failure.Code)
	encoder.u32(failure.RetryAfterMS)
	return encoder.bytes()
}

func roundTripArtifactFrame(t *testing.T, entry artifactFrame) ([]byte, error) {
	t.Helper()
	kind, payload, err := parseControlFrame(entry.frame)
	if err != nil {
		return nil, err
	}
	var encodedPayload []byte
	switch entry.label {
	case "HELLO":
		versions, invocationID, err := decodeCanonicalHello(payload)
		if err != nil {
			return nil, err
		}
		encodedPayload = encodeHello(versions, invocationID)
	case "HELLO_ACK":
		capabilities, err := decodeHelloAck(payload)
		if err != nil {
			return nil, err
		}
		encodedPayload = canonicalHelloAckPayload(capabilities.SelectedVersion, capabilities.RawBits)
	case "CONFIG_CLASSIFIER":
		config, err := decodeConfig(payload)
		if err != nil {
			return nil, err
		}
		encodedPayload = encodeConfig(config)
	case "CONFIG_EXPLICIT_HTTP", "CONFIG_EXPLICIT_WS":
		config, err := decodeConfig(payload)
		if err != nil {
			return nil, err
		}
		encodedPayload = encodeConfig(config)
	case "CREDENTIAL_ABSENT", "CREDENTIAL_PRESENT":
		credential, err := decodeCanonicalCredential(payload)
		if err != nil {
			return nil, err
		}
		encodedPayload = encodeCredential(credential)
	case "BOUND", "BOUND_WS":
		bound, err := decodeBound(payload)
		if err != nil {
			return nil, err
		}
		encodedPayload = encodeCanonicalBound(bound)
	case "READY", "SERVING", "DRAIN", "STOP", "STOPPED":
		if len(payload) != 0 {
			return nil, ErrHelperProtocol
		}
	case "CHILD_STARTED":
		identity, err := decodeCanonicalChildStarted(payload)
		if err != nil {
			return nil, err
		}
		encodedPayload = encodeChildStarted(identity)
	case "ERROR":
		code, err := decodeCanonicalError(payload)
		if err != nil {
			return nil, err
		}
		encodedPayload = canonicalErrorPayload(code)
	case "SESSION_ERROR":
		failure, err := decodeSessionError(payload)
		if err != nil {
			return nil, err
		}
		encodedPayload = canonicalSessionErrorPayload(failure)
	default:
		return nil, fmt.Errorf("unhandled canonical vector %q", entry.label)
	}
	return canonicalFrame(kind, encodedPayload), nil
}

func decodeCanonicalHello(payload []byte) ([]uint16, string, error) {
	decoder := wireDecoder{data: payload}
	count, err := decoder.u8()
	if err != nil {
		return nil, "", err
	}
	versions := make([]uint16, 0, int(count))
	for range count {
		version, err := decoder.u16()
		if err != nil {
			return nil, "", err
		}
		versions = append(versions, version)
	}
	length, err := decoder.u16()
	if err != nil || length > MaxControlString {
		return nil, "", ErrHelperProtocol
	}
	invocation, err := decoder.take(int(length))
	if err != nil || decoder.remaining() != 0 {
		return nil, "", ErrHelperProtocol
	}
	return versions, string(invocation), nil
}

func decodeCanonicalCredential(payload []byte) ([]byte, error) {
	decoder := wireDecoder{data: payload}
	present, err := decoder.u8()
	if err != nil {
		return nil, err
	}
	if present == 0 {
		if decoder.remaining() != 0 {
			return nil, ErrHelperProtocol
		}
		return nil, nil
	}
	if present != 1 {
		return nil, ErrHelperProtocol
	}
	length, err := decoder.u32()
	if err != nil || length > MaxControlCredential {
		return nil, ErrHelperProtocol
	}
	value, err := decoder.take(int(length))
	if err != nil || decoder.remaining() != 0 {
		return nil, ErrHelperProtocol
	}
	return value, nil
}

func encodeCanonicalBound(bound BoundInfo) []byte {
	var encoder wireEncoder
	encoder.string(bound.Endpoint.URL)
	encoder.u8(byte(bound.Endpoint.SecurityLevel))
	encoder.u64(bound.Endpoint.Capability)
	return encoder.bytes()
}

func decodeCanonicalChildStarted(payload []byte) (ChildIdentity, error) {
	decoder := wireDecoder{data: payload}
	pid, err := decoder.u32()
	if err != nil {
		return ChildIdentity{}, err
	}
	length, err := decoder.u32()
	if err != nil || length != 8 {
		return ChildIdentity{}, ErrHelperProtocol
	}
	identity, err := decoder.take(int(length))
	if err != nil || decoder.remaining() != 0 {
		return ChildIdentity{}, ErrHelperProtocol
	}
	return ChildIdentity{PID: int(pid), StartTime: binary.BigEndian.Uint64(identity)}, nil
}

func decodeCanonicalError(payload []byte) (uint16, error) {
	decoder := wireDecoder{data: payload}
	code, err := decoder.u16()
	if err != nil || decoder.remaining() != 0 {
		return 0, ErrHelperProtocol
	}
	return code, nil
}

func assertCanonicalConfig(t *testing.T, vectors []artifactFrame) {
	t.Helper()
	entry := findArtifactFrame(t, vectors, "CONFIG_CLASSIFIER")
	kind, payload, err := parseControlFrame(entry.frame)
	if err != nil || kind != kindConfig {
		t.Fatalf("CONFIG_CLASSIFIER frame parse: kind=%v err=%v", kind, err)
	}
	config, err := decodeConfig(payload)
	if err != nil {
		t.Fatalf("CONFIG_CLASSIFIER decode: %v", err)
	}
	if config.Deadline != SQLDClassifierDeadline || config.HeaderLimit != SQLDHeaderLimit || config.FieldLimit != SQLDFieldLimit || config.Pre200Limit != SQLDPre200Limit {
		t.Fatalf("decoded CONFIG limits=%+v, want deadline=%s header=%d field=%d pre200=%d", config, SQLDClassifierDeadline, SQLDHeaderLimit, SQLDFieldLimit, SQLDPre200Limit)
	}
	if !config.RequireToken {
		t.Fatal("decoded CONFIG unexpectedly disabled token requirement")
	}
}

func assertCanonicalChildStarted(t *testing.T, vectors []artifactFrame) {
	t.Helper()
	entry := findArtifactFrame(t, vectors, "CHILD_STARTED")
	kind, payload, err := parseControlFrame(entry.frame)
	if err != nil || kind != kindChildStarted {
		t.Fatalf("CHILD_STARTED frame parse: kind=%v err=%v", kind, err)
	}
	decoder := wireDecoder{data: payload}
	pid, err := decoder.u32()
	if err != nil {
		t.Fatalf("CHILD_STARTED pid: %v", err)
	}
	identityLength, err := decoder.u32()
	if err != nil {
		t.Fatalf("CHILD_STARTED identity length: %v", err)
	}
	identity, err := decoder.take(int(identityLength))
	if err != nil || decoder.remaining() != 0 {
		t.Fatalf("CHILD_STARTED identity payload: len=%d err=%v remaining=%d", identityLength, err, decoder.remaining())
	}
	if pid != 4242 || identityLength != 8 || binary.BigEndian.Uint64(identity) != 0x0102030405060708 {
		t.Fatalf("CHILD_STARTED=%d/%d/%x, want pid=4242 identity_len=8 identity=0102030405060708", pid, identityLength, identity)
	}
}

func assertArtifactRejections(t *testing.T, rejections []artifactFrame) {
	t.Helper()
	wantLabels := []string{
		"UNKNOWN_VERSION",
		"UNKNOWN_KIND",
		"SHORT_FRAME",
		"OVERSIZE_FRAME",
		"CONFIG_INVALID_DEADLINE",
		"CONFIG_TRAILING",
		"CONFIG_INVALID_CLASSIFIER_EXPLICIT",
		"CONFIG_INVALID_EXPLICIT_EMPTY",
		"CONFIG_INVALID_PROFILE",
		"CONFIG_INVALID_GATEWAY_ADDRESS_PRESENCE",
		"CONFIG_NO_TRUST_ROOTS",
		"HELLO_ACK_MISSING_EXPLICIT_CAPABILITY",
		"HELLO_ACK_OLD_HELPER_DOWNGRADE",
		"SESSION_ERROR_LOWERCASE_CODE",
		"SESSION_ERROR_AFTER_CONNECT_RETRY",
		"SESSION_ERROR_TRAILING",
	}
	if len(rejections) != len(wantLabels) {
		t.Fatalf("rejection count=%d, want %d", len(rejections), len(wantLabels))
	}
	for i, entry := range rejections {
		if entry.label != wantLabels[i] {
			t.Fatalf("rejection[%d] label=%q, want %q", i, entry.label, wantLabels[i])
		}
		kind, payload, frameErr := parseControlFrame(entry.frame)
		switch entry.label {
		case "UNKNOWN_VERSION", "UNKNOWN_KIND", "SHORT_FRAME", "OVERSIZE_FRAME":
			if frameErr == nil {
				t.Fatalf("rejection %s unexpectedly passed production frame validation", entry.label)
			}
		case "CONFIG_INVALID_DEADLINE", "CONFIG_TRAILING", "CONFIG_INVALID_CLASSIFIER_EXPLICIT", "CONFIG_INVALID_EXPLICIT_EMPTY", "CONFIG_INVALID_PROFILE", "CONFIG_INVALID_GATEWAY_ADDRESS_PRESENCE", "CONFIG_NO_TRUST_ROOTS":
			if frameErr != nil || kind != kindConfig {
				t.Fatalf("rejection %s framing: kind=%v err=%v", entry.label, kind, frameErr)
			}
			if _, err := decodeConfig(payload); err == nil {
				t.Fatalf("rejection %s unexpectedly passed production CONFIG validation", entry.label)
			}
		case "HELLO_ACK_MISSING_EXPLICIT_CAPABILITY":
			if frameErr != nil || kind != kindHelloAck {
				t.Fatalf("rejection %s framing: kind=%v err=%v", entry.label, kind, frameErr)
			}
			capabilities, err := decodeHelloAck(payload)
			if err != nil {
				t.Fatalf("rejection %s HELLO_ACK decode: %v", entry.label, err)
			}
			if capabilities.SQLDExplicitProfile {
				t.Fatalf("rejection %s unexpectedly advertised explicit-profile capability", entry.label)
			}
		case "HELLO_ACK_OLD_HELPER_DOWNGRADE":
			if frameErr != nil || kind != kindHelloAck {
				t.Fatalf("rejection %s framing: kind=%v err=%v", entry.label, kind, frameErr)
			}
			capabilities, err := decodeHelloAck(payload)
			if err != nil {
				t.Fatalf("rejection %s HELLO_ACK decode: %v", entry.label, err)
			}
			if capabilities.SelectedVersion == HelperContractVersion {
				t.Fatalf("rejection %s did not downgrade the helper contract", entry.label)
			}
		case "SESSION_ERROR_LOWERCASE_CODE", "SESSION_ERROR_AFTER_CONNECT_RETRY", "SESSION_ERROR_TRAILING":
			if frameErr != nil || kind != kindSessionError {
				t.Fatalf("rejection %s framing: kind=%v err=%v", entry.label, kind, frameErr)
			}
			if _, err := decodeSessionError(payload); err == nil {
				t.Fatalf("rejection %s unexpectedly passed production SESSION_ERROR validation", entry.label)
			}
		default:
			t.Fatalf("unhandled rejection %s", entry.label)
		}
	}
}

func findArtifactFrame(t *testing.T, frames []artifactFrame, label string) artifactFrame {
	t.Helper()
	for _, frame := range frames {
		if frame.label == label {
			return frame
		}
	}
	t.Fatalf("missing artifact vector %s", label)
	return artifactFrame{}
}

func fnv1a64(frames []artifactFrame) string {
	hash := uint64(0xcbf29ce484222325)
	// Keep parity with sdk-rust/src/control.rs::digest_fnv1a64. The checked-in
	// Rust literal is 0x1000_0000_01b3 (0x1000000001b3), which is part of the
	// frozen artifact digest contract even though it is not the usual FNV-1a64
	// prime spelling.
	const rustDigestPrime uint64 = 0x1000000001b3
	for _, frame := range frames {
		for _, value := range append(append([]byte(nil), frame.label...), 0) {
			hash ^= uint64(value)
			hash *= rustDigestPrime
		}
		for _, value := range frame.frame {
			hash ^= uint64(value)
			hash *= rustDigestPrime
		}
	}
	return fmt.Sprintf("%016x", hash)
}
