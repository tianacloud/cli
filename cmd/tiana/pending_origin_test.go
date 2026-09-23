package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// A deployment switch must not discard another deployment's unresolved write.
func TestCreateCommandsPreserveDifferentOriginPending(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		args          []string
	}{
		{"git", "git.create", []string{"git", "create", "pending-repo"}},
		{"sqlite", "db.create", []string{"sqlite", "create", "pending-db"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls, writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet {
					writes.Add(1)
				}
				if r.URL.Path == "/api/v1/app-types" {
					_, _ = io.WriteString(w, appTypesResponse("sqlite", "git"))
					return
				}
				http.Error(w, `{"error":{"code":"UNAVAILABLE"}}`, http.StatusServiceUnavailable)
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_pending")
			original, err := json.Marshal(map[string]any{
				"command": tc.command, "args": tc.args[1:],
				"origin": "https://previous.example.test", "user_id": "usr_pending",
				"idempotency_key": "original-create-key", "creation_operation_id": "original-operation",
			})
			if err != nil {
				t.Fatal(err)
			}
			original = append(original, '\n')
			if err := os.WriteFile(env.pendingPath, original, 0600); err != nil {
				t.Fatal(err)
			}
			var output, diagnostics bytes.Buffer
			status := runCLI(context.Background(), tc.args, strings.NewReader(""), &output, &diagnostics)
			after, err := os.ReadFile(env.pendingPath)
			if status != 1 || calls.Load() != 0 || writes.Load() != 0 || err != nil || !bytes.Equal(after, original) {
				t.Fatalf("different-origin pending not preserved: status=%d calls=%d writes=%d unchanged=%t read_error=%v diagnostics=%s", status, calls.Load(), writes.Load(), bytes.Equal(after, original), err, &diagnostics)
			}
		})
	}
}
