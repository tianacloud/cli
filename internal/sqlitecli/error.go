// Package sqlitecli implements the SQLite shell using sdk-go-sqlite.
package sqlitecli

import (
	"context"
	"encoding/json"
)

type Error struct {
	incomplete bool
	Code       string `json:"code"`
	Outcome    string `json:"outcome"`
	Message    string `json:"message"`
	ExitCode   int    `json:"exit_code"`
	Statement  int    `json:"statement,omitempty"`
	Cleanup    string `json:"cleanup,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
}

func (e *Error) Error() string { b, _ := json.Marshal(e); return string(b) }
func failure(code, message string, exit int) *Error {
	outcome := "failed"
	if exit == 5 {
		outcome = "unknown"
	}
	return &Error{Code: code, Message: message, ExitCode: exit, Outcome: outcome}
}
func inputError(message string) *Error { return failure("INPUT_ERROR", message, 2) }
func unknown(message string) *Error    { return failure("OUTCOME_UNKNOWN", message, 5) }
func outputError() *Error {
	return failure("OUTPUT_FAILED", "output incomplete; SQL may already have committed", 6)
}
func interrupted(sent bool) *Error {
	e := failure("INTERRUPTED", "interrupted; server cancellation is not confirmed", 130)
	e.Outcome = "not_sent"
	if sent {
		e.Outcome = "unknown"
	}
	return e
}

func contextError(ctx context.Context, sent bool) *Error {
	if ctx.Err() == context.Canceled {
		return interrupted(sent)
	}
	if sent {
		return unknown("response deadline exceeded; execution may have committed")
	}
	e := failure("CONNECT_TIMEOUT", "deadline exceeded before SQL was sent", 3)
	e.Outcome = "not_sent"
	return e
}
