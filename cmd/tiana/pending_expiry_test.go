package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTokenCommandsPreserveUnreadablePendingOperation(t *testing.T) {
	for _, args := range [][]string{{"create", "pending-db"}, {"tokens", "create", testInstanceID}} {
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Error(w, "unexpected request", http.StatusServiceUnavailable)
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_pending")
			original := []byte(`{"command":"db.tokens.create","operation_id":"op-pending",`)
			if err := os.WriteFile(env.pendingPath, original, 0600); err != nil {
				t.Fatal(err)
			}
			var output, diagnostics bytes.Buffer
			status := runSQLite(context.Background(), args, strings.NewReader(""), &output, &diagnostics)
			after, err := os.ReadFile(env.pendingPath)
			if status != 1 || err != nil || !bytes.Equal(after, original) || calls.Load() != 0 {
				t.Fatalf("pending operation was not preserved: status=%d calls=%d read_error=%v", status, calls.Load(), err)
			}
		})
	}
}
