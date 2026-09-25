package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tianacloud/cli/internal/diagnostics"
	"github.com/tianacloud/sdk-go/auth"
)

func TestMissingInstanceErrorRetainsRequestsWithoutDiagnosticOption(t *testing.T) {
	for _, engine := range []string{"sqlite", "git"} {
		t.Run(engine, func(t *testing.T) {
			t.Setenv("TIANA_DIAGNOSTICS", "0")
			var ids []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ids = append(ids, r.Header.Get("X-Request-ID"))
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/v1/instances" {
					_, _ = w.Write([]byte(`{"items":[],"total":0,"page":1,"page_size":20}`))
					return
				}
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"code":"INSTANCE_NOT_FOUND","message":"instance not found"}}`))
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "diagnostic-user")
			var output, stderr bytes.Buffer
			status := runCLI(context.Background(), []string{engine, "show", "missing"}, strings.NewReader(""), &output, &stderr)
			if status != 1 || len(ids) != 2 || output.Len() != 0 || !strings.Contains(stderr.String(), "instance not found") {
				t.Fatalf("status=%d requests=%d stdout=%q stderr=%q", status, len(ids), output.String(), stderr.String())
			}
			for _, id := range ids {
				if id == "" || !strings.Contains(stderr.String(), "Request ID: "+id) {
					t.Fatalf("missing request ID %q in %q", id, stderr.String())
				}
			}
		})
	}
}

func TestDiagnosticOptionWritesToStderr(t *testing.T) {
	t.Setenv("TIANA_DIAGNOSTICS", "1")
	var output, stderr bytes.Buffer
	status := runCLIWithSQL(context.Background(), []string{"sqlite", "shell", "example"}, strings.NewReader(""), &output, &stderr,
		func(ctx context.Context, _ sqliteOptions) int {
			diagnostics.Write(ctx, "sqlite", "req-success")
			return 0
		})
	if status != 0 || output.Len() != 0 || stderr.String() != "Request ID: req-success (sqlite)\n" {
		t.Fatalf("unexpected diagnostic routing: status=%d stdout=%q stderr=%q", status, output.String(), stderr.String())
	}
}

func TestCommandErrorShowsMGRRequestIdentity(t *testing.T) {
	apiError := &auth.APIError{Status: 503, Code: "UNAVAILABLE", RequestID: "req-mgr-command"}
	for _, err := range []error{apiError, errors.Join(auth.ErrAuthenticationRequired, apiError)} {
		var output bytes.Buffer
		writeCommandError(&output, err)
		if !strings.Contains(output.String(), "Request ID: req-mgr-command") {
			t.Fatalf("request identity missing from CLI error: %s", output.String())
		}
	}
}
