package sqlitecli

import tianasqlite "github.com/tianacloud/sdk-go-sqlite"

// Keep CLI output's established wire representation, using the SDK codec.
const MaxBytes = 8 * 1024 * 1024

type Column = tianasqlite.Column
type Value = tianasqlite.Value
type Result = tianasqlite.Result

// Used only to bound encoded input during preflight, before any SQL is sent.
type statementRequest struct {
	SQL      string `json:"sql"`
	WantRows bool   `json:"want_rows"`
}
type streamRequest struct {
	Type      string            `json:"type"`
	Statement *statementRequest `json:"stmt,omitempty"`
}

func executeRequest(query string, rows bool) streamRequest {
	return streamRequest{Type: "execute", Statement: &statementRequest{SQL: query, WantRows: rows}}
}
