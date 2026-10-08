package supervisor

import (
	"context"
	"crypto/subtle"
	"fmt"
	"github.com/tianacloud/cli/internal/authclient"
	"golang.org/x/net/http/httpguts"
	"io"
	"os"
	"path/filepath"
)

const (
	maxTokenIn = 128
)

// SecretToken owns a validated outer connection credential. Bytes are never exposed
// through String, Error, or formatting methods. Destroy should be called as
// soon as the helper handoff/child lifecycle no longer needs the value.
type SecretToken struct {
	bytes []byte
}

func ParseToken(value []byte) (*SecretToken, error) {
	if len(value) == 0 || !httpguts.ValidHeaderFieldValue(string(value)) {
		return nil, ErrInvalidToken
	}
	owned := append([]byte(nil), value...)
	return &SecretToken{bytes: owned}, nil
}

func (t *SecretToken) BytesForHandoff() []byte {
	if t == nil {
		return nil
	}
	return t.bytes
}

func (t *SecretToken) Destroy() {
	if t == nil {
		return
	}
	for i := range t.bytes {
		t.bytes[i] = 0
	}
	t.bytes = nil
}

func (t *SecretToken) String() string   { return "SecretToken([REDACTED])" }
func (t *SecretToken) GoString() string { return t.String() }

// Equal compares tokens without exposing them to callers.
func (t *SecretToken) Equal(other *SecretToken) bool {
	if t == nil || other == nil {
		return t == other
	}
	if len(t.bytes) != len(other.bytes) {
		return false
	}
	return subtle.ConstantTimeCompare(t.bytes, other.bytes) == 1
}

func ReadCredential(source CredentialSource, input io.Reader) (*SecretToken, error) {
	if source.Value == "" && source.Kind != CredentialFromStdin {
		return nil, fmt.Errorf("credential source is empty")
	}
	var value []byte
	defer func() { zeroBytes(value) }()
	switch source.Kind {
	case CredentialFromEnvironment:
		envValue, ok := os.LookupEnv(source.Value)
		if !ok {
			return nil, nil
		}
		value = []byte(envValue)
	case CredentialFromFile:
		read, err := readTokenFile(source.Value)
		if err != nil {
			return nil, err
		}
		value = read
	case CredentialFromStdin:
		if input == nil {
			return nil, fmt.Errorf("credential stdin is unavailable")
		}
		read, err := readCredentialLine(input)
		value = read
		if err != nil {
			return nil, fmt.Errorf("read credential stdin: %w", err)
		}
	default:
		return nil, fmt.Errorf("unknown credential source")
	}
	value = trimOneLineEnding(value)
	if len(value) == 0 {
		return nil, nil
	}
	token, err := ParseToken(value)
	if err != nil {
		return nil, err
	}
	return token, nil
}

// readCredentialLine consumes exactly one line without buffered read-ahead so
// the native client can inherit and continue reading the remaining stdin.
func readCredentialLine(input io.Reader) ([]byte, error) {
	value := make([]byte, 0, maxTokenIn)
	var one [1]byte
	for {
		n, err := input.Read(one[:])
		if n == 1 {
			if one[0] == '\n' {
				if len(value) > 0 && value[len(value)-1] == '\r' {
					value = value[:len(value)-1]
				}
				return value, nil
			}
			value = append(value, one[0])
			if len(value) > maxTokenIn {
				zeroBytes(value)
				return nil, fmt.Errorf("credential input is too large")
			}
		}
		if err != nil {
			if err == io.EOF {
				return value, nil
			}
			zeroBytes(value)
			return nil, err
		}
		if n == 0 {
			zeroBytes(value)
			return nil, io.ErrNoProgress
		}
	}
}

func trimOneLineEnding(value []byte) []byte {
	if len(value) > 0 && value[len(value)-1] == '\n' {
		value = value[:len(value)-1]
		if len(value) > 0 && value[len(value)-1] == '\r' {
			value = value[:len(value)-1]
		}
	}
	return value
}

func readTokenFile(path string) ([]byte, error) {
	if path == "" {
		return nil, ErrCredentialPermissions
	}
	return readTokenFileSecure(filepath.Clean(path))
}

// Resolve public connection credentials without prompting or touching a Token cache.
func readConnectCredential(ctx context.Context, options ConnectOptions, input io.Reader) (*SecretToken, error) {
	if options.TokenSourceSet || options.Credential != DefaultCredentialSource() {
		token, err := ReadCredential(options.Credential, input)
		if err == nil && token == nil {
			return nil, missingCredentialError()
		}
		return token, err
	}
	value, err := authclient.ConnectionCredential(ctx)
	if err != nil {
		return nil, err
	}
	defer clear(value)
	return ParseToken(value)
}

func missingCredentialError() error {
	return fmt.Errorf("connection Token is required; set TIANA_TOKEN, set TIANA_TOKEN_FILE, or run tiana login")
}

// Endpoint routing does not change credential precedence.
func readConnectEndpointCredential(ctx context.Context, options ConnectOptions, input io.Reader, _ Endpoint) (*SecretToken, error) {
	return readConnectCredential(ctx, options, input)
}
