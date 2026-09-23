package sqlitecli

import (
	"context"
	"io"
	"os"
	"strings"

	tiana "github.com/tianacloud/sdk-go"
)

func ReadTokenContext(ctx context.Context, kind, value string, input io.Reader) (*tiana.Token, *Error) {
	if ctx.Err() != nil {
		return nil, interrupted(false)
	}
	if kind != "stdin" {
		return ReadToken(kind, value, input)
	}
	type result struct {
		token *tiana.Token
		err   *Error
	}
	done := make(chan result, 1)
	go func() { token, e := ReadToken(kind, value, input); done <- result{token, e} }()
	select {
	case <-ctx.Done():
		return nil, interrupted(false)
	case r := <-done:
		return r.token, r.err
	}
}

// ReadToken consumes exactly the first stdin line; no buffered read-ahead may
// steal subsequent piped SQL. Explicit sources never fall back to another one.
func ReadToken(kind, value string, input io.Reader) (*tiana.Token, *Error) {
	var raw []byte
	switch kind {
	case "env":
		raw = []byte(os.Getenv(value))
	case "file":
		var err error
		raw, err = readTokenFile(value)
		if err != nil {
			return nil, inputError("Token file must be current-user-owned, mode 0600, regular and not a symlink")
		}
	case "stdin":
		if input == nil {
			return nil, inputError("Token stdin unavailable")
		}
		var b [1]byte
		for {
			n, err := input.Read(b[:])
			if n > 0 {
				if b[0] == '\n' {
					break
				}
				raw = append(raw, b[0])
				if len(raw) > 128 {
					clear(raw)
					return nil, inputError("Token input exceeds limit")
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil || n == 0 {
				clear(raw)
				return nil, inputError("cannot read Token input")
			}
		}
	default:
		return nil, inputError("invalid Token source")
	}
	defer clear(raw)
	text := string(raw)
	if kind == "stdin" {
		text = strings.TrimSuffix(text, "\r")
	} else if strings.HasSuffix(text, "\n") {
		text = strings.TrimSuffix(strings.TrimSuffix(text, "\n"), "\r")
	}
	if text == "" {
		return nil, inputError("TIANA_TOKEN is empty; unset it to use the signed-in account access token")
	}
	token, err := tiana.NewToken(text)
	if err != nil {
		return nil, inputError("invalid InstanceToken")
	}
	return token, nil
}
