package sqlitecli

import (
	"context"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"testing"
)

// Run under a real pseudo-terminal via scripts/test-readline-pty.py. Dot
// commands and editing must not dial a Gateway, even on Ctrl-C or EOF.
func TestShellPTYHelper(t *testing.T) {
	if os.Getenv("TIANA_TEST_SHELL_PTY") != "1" {
		t.Skip("requires PTY driver")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	c, _ := peerClient(t, func(peerRequest, int) string { t.Error("unexpected SQL during editing"); return "" })
	defer c.Close()
	err := Shell(ctx, c, NewOutput(os.Stdout, "table"), os.Stdin, os.Stderr, true)
	if ctx.Err() != nil {
		if err == nil || err.ExitCode != 130 {
			t.Fatal("cancellation did not exit cleanly")
		}
	} else if err != nil {
		t.Fatal(err)
	}
}

func TestShellDoesNotRewriteControlBytesInPipedSQL(t *testing.T) {
	sql := "SELECT '\x1b[A';"
	seen := make(chan string, 1)
	c, _ := peerClient(t, func(req peerRequest, _ int) string {
		seen <- req.Requests[0].Statement.SQL
		return httpReply(successResponse(req, "next"))
	})
	if err := Shell(context.Background(), c, NewOutput(io.Discard, "table"), strings.NewReader(sql+"\n.quit\n"), io.Discard, false); err != nil {
		t.Fatal(err)
	}
	if got := <-seen; got != sql {
		t.Fatalf("SQL source was changed: %q", got)
	}
}

func TestShellRecoveryPTYHelper(t *testing.T) {
	if os.Getenv("TIANA_TEST_SHELL_PTY") != "1" {
		t.Skip("requires PTY driver")
	}
	var mu sync.Mutex
	var queries []string
	counts := map[string]int{}
	auto := true
	c, _ := peerClient(t, func(req peerRequest, _ int) string {
		mu.Lock()
		defer mu.Unlock()
		if req.Requests[0].Statement == nil {
			return httpReply(successResponse(req, "next"))
		}
		q := strings.TrimSpace(req.Requests[0].Statement.SQL)
		queries = append(queries, q)
		counts[q]++
		if q == "SELECT 2;" && counts[q] == 1 || q == "INSERT INTO expired VALUES(1);" {
			return batonRejection()
		}
		if q == "SELECT 3;" {
			return ""
		}
		if req.Baton == nil {
			auto = true
		}
		if q == "BEGIN;" {
			auto = false
		}
		if q == "ROLLBACK;" {
			auto = true
		}
		body := successResponse(req, "next")
		if q == "SELECT missing;" {
			body = `{"baton":"next","results":[{"type":"error","error":{"code":"SQLITE_ERROR","message":"private peer message"}},{"type":"ok","response":{"type":"get_autocommit","is_autocommit":true}}]}`
		}
		if !auto {
			body = strings.ReplaceAll(body, `"is_autocommit":true`, `"is_autocommit":false`)
		}
		return httpReply(body)
	})
	err := Shell(context.Background(), c, NewOutput(os.Stdout, "table"), os.Stdin, os.Stderr, true)
	if err != nil {
		t.Fatal(err)
	}
	if e := c.Finish(context.Background(), nil); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	defer mu.Unlock()
	want := "SELECT 1;|SELECT 2;|SELECT 2;|BEGIN;|SELECT missing;|ROLLBACK;|SELECT 3;|SELECT 4;|BEGIN;|INSERT INTO expired VALUES(1);|SELECT 4;|SELECT missing;|SELECT 4;"
	if strings.Join(queries, "|") != want {
		t.Fatalf("SQL sequence: %v", queries)
	}
}
