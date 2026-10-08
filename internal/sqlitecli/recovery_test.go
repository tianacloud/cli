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
			if strings.HasPrefix(input, "insert") {
				want = 0
			}
			if requests != want || dials.Load() != int32(want) {
				t.Fatalf("requests=%d dials=%d", requests, dials.Load())
			}
		})
	}
}
