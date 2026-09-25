package sqlitecli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type peerRequest struct {
	Baton    *string         `json:"baton"`
	Requests []streamRequest `json:"requests"`
}

const emptyResult = `{"cols":[],"rows":[],"affected_row_count":0,"last_insert_rowid":null}`

func successResponse(req peerRequest, baton string) string {
	results := make([]string, 0, len(req.Requests))
	closed := false
	auto := true
	for _, r := range req.Requests {
		if r.Statement != nil && strings.HasPrefix(r.Statement.SQL, "BEGIN") {
			auto = false
		}
	}
	for _, r := range req.Requests {
		if r.Type == "close" {
			results = append(results, `{"type":"ok","response":{"type":"close"}}`)
			closed = true
		} else if r.Type == "get_autocommit" {
			results = append(results, fmt.Sprintf(`{"type":"ok","response":{"type":"get_autocommit","is_autocommit":%t}}`, auto))
		} else {
			results = append(results, `{"type":"ok","response":{"type":"execute","result":`+emptyResult+`}}`)
		}
	}
	b := `"` + baton + `"`
	if closed {
		b = "null"
	}
	return `{"baton":` + b + `,"base_url":null,"results":[` + strings.Join(results, ",") + `]}`
}
func rollbackResponse(baton string) string {
	return `{"baton":"` + baton + `","results":[{"type":"error","error":{"code":"SQLITE_ERROR","message":"cannot rollback - no transaction is active"}}]}`
}

// Native peers exercise the actual SDK TLS/H2 and Hrana HTTP exchange.
func peerClient(t *testing.T, handle func(peerRequest, int) string) (*Client, *atomic.Int32) {
	t.Helper()
	config, count := nativeGateway(t, func(r io.Reader, w io.Writer) {
		reader := bufio.NewReader(r)
		for i := 0; ; i++ {
			request, err := readPeer(reader)
			if err != nil {
				return
			}
			response := handle(request, i)
			if response == "" {
				return
			}
			if _, err = io.WriteString(w, response); err != nil {
				return
			}
		}
	}, false)
	client := NewClient(config, time.Second)
	t.Cleanup(client.Close)
	return client, count
}
func readPeer(reader *bufio.Reader) (peerRequest, error) {
	var r peerRequest
	// Parse through net/http, including its request framing, in the fixture.
	req, err := readHTTPRequest(reader)
	if err != nil {
		return r, err
	}
	defer req.Body.Close()
	if req.URL.Path != "/v3/pipeline" || req.Header.Get("Authorization") != "" || req.Header.Get("Proxy-Authorization") != "" {
		return r, fmt.Errorf("wrong inner request")
	}
	err = json.NewDecoder(req.Body).Decode(&r)
	return r, err
}
func httpReply(body string) string {
	return fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
}

func TestSerialBatonAndAutocommitExit(t *testing.T) {
	var queries []string
	c, dials := peerClient(t, func(req peerRequest, i int) string {
		if i == 0 && req.Baton != nil {
			t.Error("unexpected initial baton")
		}
		if i > 0 && (req.Baton == nil || *req.Baton != fmt.Sprint(i)) {
			t.Errorf("baton not rotated at request %d", i)
		}
		for _, r := range req.Requests {
			if r.Statement != nil {
				queries = append(queries, r.Statement.SQL)
			}
		}
		return httpReply(successResponse(req, fmt.Sprint(i+1)))
	})
	for _, sql := range []string{"SELECT 1", "SELECT 2"} {
		if _, e := c.Execute(context.Background(), sql, false); e != nil {
			t.Fatal(e)
		}
	}
	if e := c.Finish(context.Background(), nil); e != nil {
		t.Fatal(e)
	}
	if dials.Load() != 1 || strings.Join(queries, ";") != "SELECT 1;SELECT 2" {
		t.Fatal("unexpected reconnect or query sequence", queries)
	}
}
func TestLostCommitNeverReplayed(t *testing.T) {
	var count atomic.Int32
	c, dials := peerClient(t, func(req peerRequest, i int) string {
		count.Add(1)
		if i == 1 {
			if req.Requests[0].Statement.SQL != "COMMIT" {
				t.Error("wrong query")
			}
			return ""
		}
		return httpReply(successResponse(req, "1"))
	})
	if _, e := c.Execute(context.Background(), "BEGIN", false); e != nil {
		t.Fatal(e)
	}
	_, e := c.Execute(context.Background(), "COMMIT", false)
	if e == nil || e.ExitCode != 5 {
		t.Fatalf("lost commit: %v", e)
	}
	if e.RequestID == "" || e.RequestID != c.session.RequestID() {
		t.Fatalf("lost commit discarded CONNECT identity: %v", e)
	}
	e = c.Finish(context.Background(), e)
	if e.Cleanup != "unconfirmed" || count.Load() != 2 || dials.Load() != 1 {
		t.Fatalf("replayed or misleading cleanup: %v", e)
	}
}
func TestActiveTransactionRollbackFailsExit(t *testing.T) {
	c, _ := peerClient(t, func(req peerRequest, i int) string { return httpReply(successResponse(req, fmt.Sprint(i+1))) })
	_, e := c.Execute(context.Background(), "BEGIN", false)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Finish(context.Background(), nil); e == nil || e.ExitCode != 4 || e.Cleanup != "rolled_back" {
		t.Fatal(e)
	}
}
func TestInvalidRepliesPoisonSession(t *testing.T) {
	for name, response := range map[string]string{
		"redirect":             httpReply(`{"baton":"a","base_url":"http://evil/","results":[]}`),
		"lost body":            "HTTP/1.1 200 OK\r\nContent-Length: 40\r\n\r\n{}",
		"oversized":            "HTTP/1.1 200 OK\r\nContent-Length: 8388609\r\n\r\n",
		"oversized headers":    "HTTP/1.1 200 OK\r\nX: " + strings.Repeat("a", 33000) + "\r\n\r\n",
		"missing baton":        httpReply(`{"results":[{"type":"ok","response":{"type":"execute","result":` + emptyResult + `}}]}`),
		"width":                httpReply(`{"baton":"a","results":[{"type":"ok","response":{"type":"execute","result":{"cols":[],"rows":[[{"type":"null"}]],"affected_row_count":0}}}]}`),
		"server secret":        httpReply(`{"baton":"a","results":[{"type":"error","error":{"code":"SECRET_SQL_TOKEN","message":"sensitive SQL"}}]}`),
		"unacknowledged close": httpReply(`{"baton":null,"results":[{"type":"ok","response":{"type":"execute","result":` + emptyResult + `}},{"type":"error","error":{"code":"SQLITE_ERROR","message":"secret"}}]}`),
	} {
		t.Run(name, func(t *testing.T) {
			c, dials := peerClient(t, func(peerRequest, int) string { return response })
			_, e := c.Execute(context.Background(), "SELECT 1", name == "unacknowledged close")
			if e == nil || e.ExitCode != 5 || !c.poisoned {
				t.Fatalf("%v poisoned=%v", e, c.poisoned)
			}
			if strings.Contains(e.Error(), "SECRET") || strings.Contains(e.Error(), "sensitive") {
				t.Fatal("diagnostics leaked")
			}
			_ = c.Finish(context.Background(), e)
			if dials.Load() != 1 {
				t.Fatal("redial")
			}
		})
	}
}
func TestChunkedBodyAndShortExec(t *testing.T) {
	c, _ := peerClient(t, func(req peerRequest, _ int) string {
		if len(req.Requests) != 3 || req.Requests[2].Type != "close" {
			t.Error("exec did not close in same pipeline")
		}
		b := successResponse(req, "")
		return fmt.Sprintf("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n%x\r\n%s\r\n0\r\n\r\n", len(b), b)
	})
	if _, e := c.Execute(context.Background(), "SELECT 1", true); e != nil {
		t.Fatal(e)
	}
	if e := c.Finish(context.Background(), nil); e != nil {
		t.Fatal(e)
	}
}
func TestCancellationAfterSend(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	c, _ := peerClient(t, func(peerRequest, int) string { close(started); <-release; return "" })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan *Error, 1)
	go func() { _, e := c.Execute(ctx, "INSERT INTO t VALUES(1)", false); done <- e }()
	<-started
	cancel()
	select {
	case e := <-done:
		if e == nil || e.ExitCode != 130 || e.Outcome != "unknown" {
			t.Fatal(e)
		}
		if e.RequestID == "" || e.RequestID != c.session.RequestID() {
			t.Fatalf("cancelled execution discarded CONNECT identity: %v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel blocked")
	}
}
func TestOutputFailureRollsBack(t *testing.T) {
	c, _ := peerClient(t, func(req peerRequest, i int) string { return httpReply(successResponse(req, fmt.Sprint(i+1))) })
	_, e := c.Execute(context.Background(), "BEGIN", false)
	if e != nil {
		t.Fatal(e)
	}
	e = c.Finish(context.Background(), outputError())
	if e.ExitCode != 6 || e.Cleanup != "rolled_back" {
		t.Fatal(e)
	}
}
func TestIdleShellCancellation(t *testing.T) {
	c, _ := peerClient(t, func(peerRequest, int) string { t.Error("unexpected SQL"); return "" })
	in, out := io.Pipe()
	defer in.Close()
	defer out.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *Error, 1)
	go func() { done <- Shell(ctx, c, NewOutput(io.Discard, "table"), in, &bytes.Buffer{}, false) }()
	cancel()
	select {
	case e := <-done:
		if e == nil || e.ExitCode != 130 {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("idle cancel blocked")
	}
}

func TestExpiredBatonNeverRestored(t *testing.T) {
	c, dials := peerClient(t, func(req peerRequest, i int) string {
		if i == 0 {
			return httpReply(successResponse(req, "first"))
		}
		body := `{"code":"STREAM_EXPIRED","message":"secret server detail"}`
		return fmt.Sprintf("HTTP/1.1 410 Gone\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	})
	if _, e := c.Execute(context.Background(), "SELECT 1", false); e != nil {
		t.Fatal(e)
	}
	_, e := c.Execute(context.Background(), "SELECT 2", false)
	if e == nil || e.ExitCode != 3 || e.Code != "STREAM_EXPIRED" {
		t.Fatal(e)
	}
	e = c.Finish(context.Background(), e)
	if dials.Load() != 1 || e.Cleanup != "unconfirmed" || strings.Contains(e.Error(), "secret") {
		t.Fatal("restored expired session or leaked diagnostic")
	}
}

func TestDeadlineIsNotUserInterruption(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if e := contextError(ctx, true); e.ExitCode != 5 || e.Outcome != "unknown" {
		t.Fatal(e)
	}
	if e := contextError(ctx, false); e.ExitCode != 3 || e.Outcome != "not_sent" {
		t.Fatal(e)
	}
}

func TestOneShotCleanupClassification(t *testing.T) {
	for _, lost := range []bool{true, false} {
		c, _ := peerClient(t, func(req peerRequest, _ int) string {
			if lost {
				return ""
			}
			return httpReply(`{"baton":null,"results":[{"type":"error","error":{"code":"SQLITE_CONSTRAINT","message":"hidden"}},{"type":"ok","response":{"type":"get_autocommit","is_autocommit":true}},{"type":"ok","response":{"type":"close"}}]}`)
		})
		_, e := c.Execute(context.Background(), "INSERT INTO t VALUES(1)", true)
		e = c.Finish(context.Background(), e)
		if lost {
			if e == nil || e.ExitCode != 5 || e.Cleanup != "unconfirmed" {
				t.Fatalf("lost one-shot cleanup: %v", e)
			}
		} else if e == nil || e.ExitCode != 4 || e.Cleanup != "" {
			t.Fatalf("confirmed close after SQL error: %v", e)
		}
	}
}

func TestFinishSharesCleanupDeadline(t *testing.T) {
	c, _ := peerClient(t, func(req peerRequest, _ int) string {
		if req.Requests[0].Type == "close" {
			time.Sleep(700 * time.Millisecond)
		} else if req.Requests[0].Statement.SQL == "ROLLBACK" {
			time.Sleep(200 * time.Millisecond)
		}
		return httpReply(successResponse(req, "baton"))
	})
	if _, err := c.Execute(context.Background(), "BEGIN", false); err != nil {
		t.Fatal(err)
	}
	c.timeout = 300 * time.Millisecond
	start := time.Now()
	err := c.Finish(context.Background(), nil)
	if elapsed := time.Since(start); elapsed > 600*time.Millisecond {
		t.Fatalf("cleanup allocated a second deadline: %v", elapsed)
	}
	if err == nil || err.Cleanup != "unconfirmed" {
		t.Fatalf("missing close failure: %v", err)
	}
}
