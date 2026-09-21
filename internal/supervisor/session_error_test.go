package supervisor

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func encodeSessionErrorForTest(failure SessionFailureError) []byte {
	var encoder wireEncoder
	encoder.u8(byte(failure.Phase))
	encoder.u16(failure.Status)
	encoder.shortString(failure.Code)
	encoder.u32(failure.RetryAfterMS)
	return encoder.bytes()
}

func drainingProcessHelper(frames ...[]byte) *ProcessHelper {
	var input []byte
	for _, frame := range frames {
		input = append(input, frame...)
	}
	reader := io.NopCloser(bytes.NewReader(input))
	return &ProcessHelper{
		read:   reader,
		reader: bufio.NewReader(reader),
		state:  StateDraining,
	}
}

func TestProcessHelperReturnsBoundedSessionFailureBeforeStopped(t *testing.T) {
	failure := SessionFailureError{
		Phase:        SessionFailureBeforeConnect,
		Status:       503,
		Code:         "POLICY_UNAVAILABLE",
		RetryAfterMS: 250,
	}
	helper := drainingProcessHelper(
		canonicalFrame(kindSessionError, encodeSessionErrorForTest(failure)),
		canonicalFrame(kindStopped, nil),
	)
	err := helper.WaitStopped(context.Background())
	var got SessionFailureError
	if !errors.As(err, &got) || got != failure {
		t.Fatalf("WaitStopped() err=%v", err)
	}
	if helper.state != StateStopped {
		t.Fatalf("state=%s", helper.state)
	}
	if !strings.Contains(err.Error(), "automatic retry is disabled") {
		t.Fatalf("retry safety missing from %q", err)
	}
}

func TestProcessHelperMarksPost200FailureOutcomeUnknown(t *testing.T) {
	failure := SessionFailureError{
		Phase: SessionFailureAfterConnect,
		Code:  "TUNNEL_INTERRUPTED",
	}
	helper := drainingProcessHelper(
		canonicalFrame(kindSessionError, encodeSessionErrorForTest(failure)),
		canonicalFrame(kindStopped, nil),
	)
	err := helper.WaitStopped(context.Background())
	if err == nil || !strings.Contains(err.Error(), "request outcome may be unknown") ||
		!strings.Contains(err.Error(), "automatic retry is disabled") {
		t.Fatalf("post-200 failure=%v", err)
	}
}

func TestSessionFailureExplainsGatewayTimeoutStage(t *testing.T) {
	for code, want := range map[string]string{
		"GATEWAY_TCP_TIMEOUT":      "timed out opening a TCP connection to Gateway; check Endpoint DNS, VPN/firewall access, and the configured Gateway port (code=GATEWAY_TCP_TIMEOUT)",
		"GATEWAY_TLS_TIMEOUT":      "Gateway TLS handshake timed out; check the network path and TLS interception (code=GATEWAY_TLS_TIMEOUT)",
		"GATEWAY_PROTOCOL_TIMEOUT": "Gateway HTTP/2 handshake timed out; check that the network path permits ALPN h2 (code=GATEWAY_PROTOCOL_TIMEOUT)",
		"GATEWAY_CONNECT_TIMEOUT":  "Gateway did not answer CONNECT before the timeout; check Gateway, Control, and Runtime health (code=GATEWAY_CONNECT_TIMEOUT)",
		"GATEWAY_TIMEOUT":          "Gateway connection setup timed out; check Endpoint DNS, VPN/firewall access, and the configured Gateway port, then check Gateway, Control, and Runtime health (code=GATEWAY_TIMEOUT)",
	} {
		t.Run(code, func(t *testing.T) {
			failure := SessionFailureError{Phase: SessionFailureBeforeConnect, Code: code}
			if got := failure.Error(); got != want {
				t.Fatalf("failure=%q, want %q", got, want)
			}
		})
	}
}

func TestProcessHelperRejectsMalformedOrRepeatedSessionFailure(t *testing.T) {
	valid := SessionFailureError{Phase: SessionFailureBeforeConnect, Status: 407, Code: "AUTH_REQUIRED"}
	invalidLowercase := valid
	invalidLowercase.Code = "token secret"
	invalidPost200Retry := SessionFailureError{Phase: SessionFailureAfterConnect, Code: "TUNNEL_INTERRUPTED", RetryAfterMS: 1}
	for name, payload := range map[string][]byte{
		"lowercase":         encodeSessionErrorForTest(invalidLowercase),
		"post-200-retry":    encodeSessionErrorForTest(invalidPost200Retry),
		"trailing":          append(encodeSessionErrorForTest(valid), 0),
		"unknown-phase":     encodeSessionErrorForTest(SessionFailureError{Phase: 9, Code: "AUTH_REQUIRED"}),
		"local-http-status": encodeSessionErrorForTest(SessionFailureError{Phase: SessionFailureLocalRequest, Status: 400, Code: "LOCAL_REQUEST_REJECTED"}),
	} {
		t.Run(name, func(t *testing.T) {
			helper := drainingProcessHelper(canonicalFrame(kindSessionError, payload))
			if err := helper.WaitStopped(context.Background()); !errors.Is(err, ErrHelperProtocol) {
				t.Fatalf("WaitStopped() err=%v", err)
			}
		})
	}

	frame := canonicalFrame(kindSessionError, encodeSessionErrorForTest(valid))
	helper := drainingProcessHelper(frame, frame, canonicalFrame(kindStopped, nil))
	if err := helper.WaitStopped(context.Background()); !errors.Is(err, ErrHelperProtocol) {
		t.Fatalf("repeated SESSION_ERROR err=%v", err)
	}
}
