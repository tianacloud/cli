package sqlitecli

import (
	"context"
	"io"
	"os"
	"os/signal"
	"strings"
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
