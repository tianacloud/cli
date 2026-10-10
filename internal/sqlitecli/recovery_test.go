package sqlitecli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func batonRejection() string {
	body := `{"code":"BATON_INVALID"}`
	return fmt.Sprintf("HTTP/1.1 400 Bad Request\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
}

func TestInteractiveSessionRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, first, failing string
		lost, twice          bool
		wantError            bool
		wantQueries          string
	}{
		{"expired query", "SELECT 1", "SELECT 2", false, false, false, "SELECT 1;SELECT 2;SELECT 2;SELECT 3"},
		{"expired write", "SELECT 1", "INSERT INTO t VALUES(2)", false, false, false, "SELECT 1;INSERT INTO t VALUES(2);INSERT INTO t VALUES(2);SELECT 3"},
		{"expired transaction", "BEGIN", "INSERT INTO t VALUES(2)", false, false, true, "BEGIN;INSERT INTO t VALUES(2);SELECT 3"},
		{"lost write", "SELECT 1", "INSERT INTO t VALUES(2)", true, false, true, "SELECT 1;INSERT INTO t VALUES(2);SELECT 3"},
		{"lost commit", "BEGIN", "COMMIT", true, false, true, "BEGIN;COMMIT;SELECT 3"},
		{"retry rejected", "SELECT 1", "SELECT 2", false, true, true, "SELECT 1;SELECT 2;SELECT 2;SELECT 3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var queries []string
			c, dials := peerClient(t, func(req peerRequest, _ int) string {
				if req.Requests[0].Statement == nil {
					return httpReply(successResponse(req, "next"))
				}
				mu.Lock()
				defer mu.Unlock()
				queries = append(queries, req.Requests[0].Statement.SQL)
				n := len(queries)
				if n == 2 || (tc.twice && n == 3) {
					if tc.lost {
						return ""
					}
					return batonRejection()
				}
				if (n == 3 || (tc.twice && n == 4)) && req.Baton != nil {
					t.Error("replacement reused baton")
				}
				return httpReply(successResponse(req, "next"))
			})
			var diagnostics bytes.Buffer
			if _, e := c.executeInteractive(context.Background(), tc.first, &diagnostics); e != nil {
				t.Fatal(e)
			}
			_, e := c.executeInteractive(context.Background(), tc.failing, &diagnostics)
			if (e != nil) != tc.wantError {
				t.Fatalf("error=%v", e)
			}
			if tc.lost && (e == nil || e.Outcome != "unknown") {
				t.Fatalf("unknown lost: %v", e)
			}
			if _, e = c.executeInteractive(context.Background(), "SELECT 3", &diagnostics); e != nil {
				t.Fatal(e)
			}
			if e = c.Finish(context.Background(), nil); e != nil {
				t.Fatal(e)
			}
			mu.Lock()
			got := strings.Join(queries, ";")
			mu.Unlock()
			if got != tc.wantQueries {
				t.Fatalf("queries=%s, want %s", got, tc.wantQueries)
			}
			wantDials := int32(2)
			if tc.twice {
				wantDials = 3
			}
			if dials.Load() != wantDials {
				t.Fatalf("dials=%d", dials.Load())
			}
			if !strings.Contains(diagnostics.String(), "Reconnecting...") || !strings.Contains(diagnostics.String(), "Connected.") {
				t.Fatalf("missing progress: %s", &diagnostics)
			}
		})
	}
}

func TestInteractiveRecoverySharesDeadline(t *testing.T) {
	c, dials := peerClient(t, func(req peerRequest, _ int) string {
		if req.Requests[0].Statement != nil && req.Requests[0].Statement.SQL == "SELECT 2" {
			time.Sleep(100 * time.Millisecond)
			if req.Baton != nil {
				return batonRejection()
			}
		}
		return httpReply(successResponse(req, "next"))
	})
	if _, e := c.Execute(context.Background(), "SELECT 1", false); e != nil {
		t.Fatal(e)
	}
	c.timeout = 150 * time.Millisecond
	_, e := c.executeInteractive(context.Background(), "SELECT 2", io.Discard)
	if e == nil || e.Outcome != "unknown" || dials.Load() != 2 {
		t.Fatalf("shared deadline: %v, dials=%d", e, dials.Load())
	}
}

func TestInteractiveConnectionFailureCanRetryOnNewInput(t *testing.T) {
	config, dials := nativeGateway(t, func(io.Reader, io.Writer) { t.Error("SQL sent to refused gateway") }, true)
	c := NewClient(config, time.Second)
	defer c.Close()
	var diagnostics bytes.Buffer
	for range 2 {
		if _, e := c.executeInteractive(context.Background(), "SELECT 1", &diagnostics); e == nil || e.Code != "GATEWAY_REJECTED" {
			t.Fatal(e)
		}
	}
	if dials.Load() != 2 || strings.Contains(diagnostics.String(), "Connected.") {
		t.Fatalf("dials=%d diagnostics=%s", dials.Load(), &diagnostics)
	}
	if e := c.Finish(context.Background(), nil); e != nil {
		t.Fatal(e)
	}
}

func TestInteractiveRecoveryDiagnosticFailureStopsSQL(t *testing.T) {
	c, dials := peerClient(t, func(req peerRequest, _ int) string {
		if req.Baton != nil {
			return batonRejection()
		}
		return httpReply(successResponse(req, "next"))
	})
	if _, e := c.Execute(context.Background(), "SELECT 1", false); e != nil {
		t.Fatal(e)
	}
	_, e := c.executeInteractive(context.Background(), "INSERT INTO t VALUES(2)", rejectingDiagnostics{})
	if e == nil || e.ExitCode != 6 || dials.Load() != 1 {
		t.Fatalf("output failure: %v dials=%d", e, dials.Load())
	}
}

type rejectingDiagnostics struct{}

func (rejectingDiagnostics) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestPipedShellStopsAtInputOrExecutionFailure(t *testing.T) {
	for _, input := range []string{"insert inot t values(1);\nSELECT 2;\n", "SELECT 1;\nSELECT 2;\n"} {
		t.Run(input, func(t *testing.T) {
			var requests int
			c, dials := peerClient(t, func(req peerRequest, _ int) string { requests++; return batonRejection() })
			e := Shell(context.Background(), c, NewOutput(io.Discard, "table"), strings.NewReader(input), io.Discard, false)
			if e == nil {
				t.Fatal("piped shell swallowed failure")
			}
			want := 1
			if requests != want || dials.Load() != int32(want) {
				t.Fatalf("requests=%d dials=%d", requests, dials.Load())
			}
		})
	}
}

// A grammar error must reach the server and stop the remaining script without
// replaying its preceding write. The session's confirmed transaction survives.
func TestServerParseErrorStopsScriptWithoutDiscardingSession(t *testing.T) {
	for _, transaction := range []bool{false, true} {
		var mu sync.Mutex
		var queries []string
		auto := true
		c, dials := peerClient(t, func(req peerRequest, _ int) string {
			if req.Requests[0].Statement == nil {
				return httpReply(successResponse(req, "next"))
			}
			q := strings.TrimSpace(req.Requests[0].Statement.SQL)
			mu.Lock()
			queries = append(queries, q)
			mu.Unlock()
			switch q {
			case "BEGIN;":
				auto = false
			case "ROLLBACK;":
				auto = true
			}
			body := successResponse(req, "next")
			if q == "SELECT FROM;" {
				body = `{"baton":"next","results":[{"type":"error","error":{"code":"SQL_PARSE_ERROR","message":"untrusted SQL text"}},{"type":"ok","response":{"type":"get_autocommit","is_autocommit":true}}]}`
			}
			if !auto {
				body = strings.ReplaceAll(body, `"is_autocommit":true`, `"is_autocommit":false`)
			}
			return httpReply(body)
		})
		if transaction {
			if _, e := c.Execute(context.Background(), "BEGIN;", false); e != nil {
				t.Fatal(e)
			}
		}
		parts, e := Split("INSERT INTO t VALUES(1); SELECT FROM; SELECT 99;")
		if e != nil {
			t.Fatalf("grammar was rejected locally: %v", e)
		}
		e = Script(context.Background(), c, NewOutput(io.Discard, "table"), parts)
		if e == nil || e.Code != "SQL_PARSE_ERROR" || e.ExitCode != 4 || e.Outcome != "failed" || e.Statement != 2 {
			t.Fatalf("definite server parse error misclassified: %v", e)
		}
		if state, known := c.Autocommit(); !known || state == transaction {
			t.Fatalf("session state changed: %v %v", state, known)
		}
		if _, e = c.executeInteractive(context.Background(), "SELECT 2;", io.Discard); e != nil {
			t.Fatal(e)
		}
		if transaction {
			if _, e = c.Execute(context.Background(), "ROLLBACK;", false); e != nil {
				t.Fatal(e)
			}
		}
		if e = c.Finish(context.Background(), nil); e != nil {
			t.Fatal(e)
		}
		mu.Lock()
		got := strings.Join(queries, "|")
		mu.Unlock()
		want := "INSERT INTO t VALUES(1);|SELECT FROM;|SELECT 2;"
		if transaction {
			want = "BEGIN;|" + want + "|ROLLBACK;"
		}
		if got != want || dials.Load() != 1 {
			t.Fatalf("replayed/skipped/reconnected SQL: %s connects=%d", got, dials.Load())
		}
	}
}

func TestWholeInputResourceFailureSendsNoEarlierStatements(t *testing.T) {
	for name, sql := range map[string]string{
		"encoded request":       "INSERT INTO t VALUES(1); SELECT '" + strings.Repeat("\t", MaxBytes/2) + "';\n",
		"statement count":       "INSERT INTO t VALUES(1);" + strings.Repeat("SELECT 1;", MaxStatements) + "\n",
		"late incomplete quote": "INSERT INTO t VALUES(1); SELECT 'unterminated\n",
	} {
		t.Run(name, func(t *testing.T) {
			c, connects := peerClient(t, func(peerRequest, int) string { t.Error("resource/lexical preflight sent SQL"); return "" })
			e := Shell(context.Background(), c, NewOutput(io.Discard, "table"), strings.NewReader(sql), io.Discard, false)
			if e == nil || e.Code != "INPUT_ERROR" || connects.Load() != 0 {
				t.Fatalf("preflight failed too late: %v connects=%d", e, connects.Load())
			}
		})
	}
}
