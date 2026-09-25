package sqlitecli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tianacloud/cli/internal/diagnostics"
	tiana "github.com/tianacloud/sdk-go"
	tianasqlite "github.com/tianacloud/sdk-go-sqlite"
)

func TestSuccessfulSQLDiagnosticUsesTunnelIdentity(t *testing.T) {
	client, _ := peerClient(t, func(req peerRequest, _ int) string { return httpReply(successResponse(req, "1")) })
	var output bytes.Buffer
	ctx := diagnostics.WithWriter(context.Background(), &output)
	if _, err := client.Execute(ctx, "SELECT 1", true); err != nil {
		t.Fatal(err)
	}
	id := client.session.RequestID()
	if id == "" || !strings.Contains(output.String(), "Request ID: "+id+" (sqlite)") {
		t.Fatalf("missing success identity: %q", output.String())
	}
}

func TestSessionErrorPreservesRequestIdentity(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  error
		exit int
	}{
		{"before-connect", context.Background(), &tiana.Error{Kind: tiana.TCP, RequestID: "req-connect"}, 3},
		{"sql", context.Background(), &tianasqlite.Error{Code: "SQLITE_ERROR", RequestID: "req-sql"}, 4},
		{"cancelled", cancelled, &tianasqlite.Error{Code: "REQUEST_CANCELLED", RequestID: "req-cancel", OutcomeUnknown: true}, 130},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := sessionError(tc.ctx, tc.err)
			if result.RequestID == "" || result.ExitCode != tc.exit {
				t.Fatalf("diagnostic identity or exit status lost: %+v", result)
			}
			var output map[string]any
			if err := json.Unmarshal([]byte(result.Error()), &output); err != nil || output["request_id"] != result.RequestID {
				t.Fatalf("request ID missing from printed JSON: %v %v", output, err)
			}
		})
	}
}
