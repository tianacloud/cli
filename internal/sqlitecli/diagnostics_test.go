package sqlitecli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/diagnostics"
	tiana "github.com/tianacloud/sdk-go"
	tianasqlite "github.com/tianacloud/sdk-go-sqlite"
)

func TestConnectCancellationRetainsAllocatedRequestID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var emitted bytes.Buffer
	ctx = diagnostics.WithWriter(ctx, &emitted)
	id := ""
	config := tianasqlite.Config{Gateway: tiana.Config{Endpoint: nativeEndpoint, DialAddress: "127.0.0.1:1", OnRequestID: func(value string) { id = value; cancel() }}}
	client := NewClient(config, time.Second)
	defer client.Close()
	_, failure := client.Execute(ctx, "SELECT 1", true)
	if id == "" || failure == nil || failure.ExitCode != 130 || failure.RequestID != id || !strings.Contains(emitted.String(), id) {
		t.Fatalf("allocated=%q failure=%+v diagnostics=%s", id, failure, &emitted)
	}
}

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

func TestInteractiveRecoveryKeepsBothConnectionIdentities(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "rejected-baton", true: "lost-response"}[lost], func(t *testing.T) {
			calls := 0
			client, _ := peerClient(t, func(req peerRequest, _ int) string {
				if req.Requests[0].Statement != nil {
					calls++
					if calls == 2 {
						if lost {
							return ""
						}
						return batonRejection()
					}
				}
				return httpReply(successResponse(req, "next"))
			})
			var output bytes.Buffer
			ctx := diagnostics.WithWriter(context.Background(), &output)
			if _, e := client.executeInteractive(ctx, "SELECT 1", &output); e != nil {
				t.Fatal(e)
			}
			first := client.session.RequestID()
			_, failure := client.executeInteractive(ctx, "SELECT 2", &output)
			if lost {
				if failure == nil || failure.RequestID != first || failure.Outcome != "unknown" {
					t.Fatalf("lost response identity: %+v", failure)
				}
				if _, e := client.executeInteractive(ctx, "SELECT 3", &output); e != nil {
					t.Fatal(e)
				}
			} else if failure != nil {
				t.Fatal(failure)
			}
			second := client.session.RequestID()
			if first == "" || second == "" || first == second {
				t.Fatalf("connection identities first=%q second=%q", first, second)
			}
			for _, id := range []string{first, second} {
				if !strings.Contains(output.String(), "Request ID: "+id+" (sqlite)") {
					t.Fatalf("missing diagnostic %q: %s", id, output.String())
				}
			}
			if e := client.Finish(ctx, nil); e != nil {
				t.Fatal(e)
			}
		})
	}
}
