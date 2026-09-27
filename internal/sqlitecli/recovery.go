package sqlitecli

import (
	"context"
	"fmt"
	"io"
)

// executeInteractive owns recovery policy; scripts keep the single-session API.
func (c *Client) executeInteractive(ctx context.Context, query string, diagnostics io.Writer) (*Result, *Error) {
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	reconnecting := c.poisoned
	reconnect := func() *Error {
		if _, err := fmt.Fprintln(diagnostics, "Connection lost. Reconnecting..."); err != nil {
			return outputError()
		}
		c.newSession()
		c.poisoned = false
		reconnecting = true
		return nil
	}
	if reconnecting {
		if e := reconnect(); e != nil {
			return nil, e
		}
	}
	auto, known := c.Autocommit()
	result, e := c.Execute(requestCtx, query, false)
	if e != nil && e.Code == "BATON_INVALID" && e.Outcome == "failed" && known && auto && requestCtx.Err() == nil {
		c.discardSession()
		if e := reconnect(); e != nil {
			return nil, e
		}
		result, e = c.Execute(requestCtx, query, false)
	}
	if c.poisoned {
		if e != nil {
			e.Cleanup = "unconfirmed"
			if known && !auto {
				e.Message += "; previous transaction cannot be continued"
			}
		}
		c.discardSession()
	} else if reconnecting && (e == nil || e.ExitCode == 4) {
		if _, err := fmt.Fprintln(diagnostics, "Connected. Session state has been reset (temporary tables and connection settings are not restored)."); err != nil {
			return nil, outputError()
		}
	}
	return result, e
}

func (c *Client) discardSession() {
	if c.session != nil {
		_ = c.session.Close()
	}
	c.session = nil
	c.poisoned = true
}

func (c *Client) prompt() string {
	auto, known := c.Autocommit()
	if !known {
		return "sqlite[tx=?]> "
	}
	if auto {
		return "sqlite[tx=off]> "
	}
	return "sqlite[tx=on]> "
}
