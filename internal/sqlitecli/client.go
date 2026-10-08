package sqlitecli

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"time"

	"github.com/tianacloud/cli/internal/diagnostics"
	tiana "github.com/tianacloud/sdk-go"
	tianasqlite "github.com/tianacloud/sdk-go-sqlite"
)

// Client adapts the shared SDK session to CLI diagnostics and exit cleanup.
type Client struct {
	session          *tianasqlite.Session
	config           tianasqlite.Config
	initErr          error
	requestID        *string
	timeout          time.Duration
	poisoned, closed bool
}

func NewClient(config tianasqlite.Config, timeout time.Duration) *Client {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	config.RequestTimeout = timeout
	if config.Gateway.Token != nil {
		token := *config.Gateway.Token
		config.Gateway.Token = &token
	}
	if config.Gateway.RootCAs != nil {
		config.Gateway.RootCAs = config.Gateway.RootCAs.Clone()
	}
	client := &Client{config: config, timeout: timeout}
	client.newSession()
	return client
}

func (c *Client) newSession() {
	id := new(string)
	c.requestID = id
	config := c.config
	callback := config.Gateway.OnRequestID
	config.Gateway.OnRequestID = func(value string) {
		*id = value
		if callback != nil {
			callback(value)
		}
	}
	c.session, c.initErr = tianasqlite.NewSession(config)
}
func SDKConfig(endpoint, port string, token *tiana.Token, roots *x509.CertPool, timeout time.Duration) tianasqlite.Config {
	return tianasqlite.Config{Gateway: tiana.Config{Endpoint: endpoint, Token: token, RootCAs: roots,
		DialAddress: net.JoinHostPort(endpoint, port), ConnectTimeout: timeout, ResponseTimeout: timeout, MaxStreams: 1}, RequestTimeout: timeout}
}
func (c *Client) Close() {
	if c.session != nil {
		_ = c.session.Close()
	}
	c.closed = true
}
func (c *Client) Autocommit() (bool, bool) {
	if c.session == nil || c.closed {
		return false, false
	}
	return c.session.Autocommit()
}
func (c *Client) Execute(ctx context.Context, query string, closing bool) (result *Result, failure *Error) {
	defer func() {
		id := ""
		if c.session != nil {
			id = c.session.RequestID()
		}
		if id == "" && c.requestID != nil {
			id = *c.requestID
		}
		if failure != nil {
			if failure.RequestID == "" {
				failure.RequestID = id
			}
			id = failure.RequestID
		}
		diagnostics.Write(ctx, "sqlite", id)
	}()
	if c.poisoned || c.closed {
		return nil, unknown("session is unusable; do not replay unconfirmed operations")
	}
	if c.initErr != nil {
		c.poisoned = true
		return nil, connectFailure(c.initErr)
	}
	var err error
	if closing {
		result, err = c.session.ExecuteAndClose(ctx, query)
		c.closed = true
	} else {
		result, err = c.session.Execute(ctx, query)
	}
	if err != nil {
		_, known := c.session.Autocommit()
		c.poisoned = !known
		if closing {
			var sdkErr *tianasqlite.Error
			c.poisoned = errors.As(err, &sdkErr) && sdkErr.OutcomeUnknown
		}
		return nil, sessionError(ctx, err)
	}
	return result, nil
}
func sessionError(ctx context.Context, err error) (result *Error) {
	defer func() {
		var sqlErr *tianasqlite.Error
		var gatewayErr *tiana.Error
		if errors.As(err, &sqlErr) {
			result.RequestID = sqlErr.RequestID
		}
		if result.RequestID == "" && errors.As(err, &gatewayErr) {
			result.RequestID = gatewayErr.RequestID
		}
	}()
	var sdkErr *tianasqlite.Error
	typed := errors.As(err, &sdkErr)
	sent := typed && sdkErr.OutcomeUnknown
	if ctx.Err() != nil {
		return contextError(ctx, sent)
	}
	if typed {
		switch sdkErr.Code {
		case "BATON_INVALID":
			if !sent {
				return failure(sdkErr.Code, "SQL session lost; current statement was not executed", 3)
			}
			return failure(sdkErr.Code, "SQL outcome unknown; inspect confirmed operations before retrying", 5)
		case "STREAM_EXPIRED", "STREAM_NOT_FOUND", "STREAM_LIMIT", "SERVICE_STOPPING":
			return failure(sdkErr.Code, "HTTP request rejected; session will not reconnect automatically", 3)
		case "REQUEST_TOO_LARGE":
			if sent {
				return failure(sdkErr.Code, "HTTP request rejected; session will not reconnect automatically", 3)
			}
			return inputError("encoded Hrana request exceeds 8 MiB")
		case "INVALID_SQL", "INVALID_ARGUMENT", "MIXED_ARGUMENTS":
			return inputError("invalid or oversized SQL input")
		}
		if sent {
			return failure(sdkErr.Code, "SQL outcome unknown; inspect confirmed operations before retrying", 5)
		}
		return failure(sdkErr.Code, "SQL execution failed; earlier statements may have committed", 4)
	}
	return connectFailure(err)
}
func connectFailure(err error) *Error {
	var sdkErr *tiana.Error
	if errors.As(err, &sdkErr) {
		switch sdkErr.Kind {
		case tiana.TLS:
			return failure("CONNECT_TLS", "TLS handshake or certificate verification failed; verify --ca-file and endpoint hostname", 3)
		case tiana.Gateway:
			return failure("GATEWAY_REJECTED", "Gateway rejected the tunnel; verify the InstanceToken and endpoint policy", 3)
		case tiana.HTTP2:
			return failure("CONNECT_HTTP2", "Gateway HTTP/2 negotiation failed", 3)
		case tiana.TCP:
			return failure("CONNECT_TCP", "Gateway TCP connection failed", 3)
		case tiana.Timeout:
			return failure("CONNECT_TIMEOUT", "Gateway connection timed out", 3)
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return failure("CONNECT_TIMEOUT", "Gateway connection timed out", 3)
	}
	return failure("CONNECT_FAILED", "cannot establish verified Gateway tunnel; SQL was not sent", 3)
}
func (c *Client) Finish(ctx context.Context, primary *Error) (result *Error) {
	defer func() {
		if result != nil && result.RequestID == "" && c.session != nil {
			result.RequestID = c.session.RequestID()
		}
	}()
	// Interactive recovery already reported and released the failed session.
	if c.session == nil && c.poisoned {
		return primary
	}
	if c.closed {
		if c.poisoned {
			if primary == nil {
				primary = unknown("session outcome unknown")
			}
			primary.Cleanup = "unconfirmed"
		}
		return primary
	}
	defer c.Close()
	auto, known := c.Autocommit()
	if c.poisoned || !known {
		if primary == nil {
			primary = unknown("session outcome unknown")
		}
		primary.Cleanup = "unconfirmed"
		return primary
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), min(c.timeout, 3*time.Second))
	defer cancel()
	if !auto {
		_, err := c.Execute(cleanup, "ROLLBACK", false)
		auto, known = c.Autocommit()
		if err != nil || !known || !auto {
			if primary == nil {
				primary = unknown("rollback was not acknowledged")
			}
			primary.Cleanup = "unconfirmed"
		} else {
			if primary == nil {
				primary = failure("UNCOMMITTED_TRANSACTION", "uncommitted transaction rolled back on exit", 4)
			}
			primary.Cleanup = "rolled_back"
		}
	}
	if err := c.session.CloseContext(cleanup); err != nil {
		if primary == nil {
			primary = sessionError(ctx, err)
		}
		primary.Cleanup = "unconfirmed"
	}
	return primary
}
