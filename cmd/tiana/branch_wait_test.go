package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

// These cases catch premature success, polling the wrong operation, replaying
// creation, and treating unknown/failed observations as completed creation.
func TestBranchCreateWait(t *testing.T) {
	for _, tc := range []struct {
		name, flag, mode string
		code, polls      int
	}{
		{"default", "", "success", 0, 0},
		{"false", "--wait=false", "success", 0, 0},
		{"long", "--wait", "success", 0, 1},
		{"short", "-w", "success", 0, 1},
		{"progress", "-w", "progress", 0, 4},
		{"failed", "-w", "failed", 1, 1},
		{"unknown", "-w", "unknown", 1, 1},
		{"missing", "-w", "missing", 1, 1},
		{"unavailable", "-w", "unavailable", 1, 1},
		{"malformed", "-w", "malformed", 1, 1},
		{"custom parent", "-w", "custom-parent", 0, 1},
		{"wrong operation", "-w", "operation", 1, 1},
		{"wrong instance", "-w", "instance", 1, 1},
		{"wrong kind", "-w", "kind", 1, 1},
		{"wrong parent", "-w", "parent", 1, 1},
		{"missing child", "-w", "child", 1, 1},
		{"cancel", "-w", "cancel", 130, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			writes, polls := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				const base = "/api/v1/instances/inst_cli"
				switch r.URL.Path {
				case base:
					io.WriteString(w, `{"id":"inst_cli","engine":"sqlite"}`)
				case base + "/branches":
					io.WriteString(w, `{"items":[{"branch_id":"source","name":"dev"}]}`)
				case base + "/branches/source":
					io.WriteString(w, `{"instance_id":"inst_cli","branch":{"branch_id":"source","name":"dev"}}`)
				case base + "/branches/main":
					io.WriteString(w, `{"instance_id":"inst_cli","branch":{"branch_id":"main","name":"main"}}`)
				case base + "/branches/main/children", base + "/branches/source/children":
					writes++
					if tc.mode == "custom-parent" && r.URL.Path != base+"/branches/source/children" {
						t.Error("wrong parent")
					}
					if r.Method != "POST" {
						t.Errorf("method=%s", r.Method)
					}
					w.WriteHeader(202)
					io.WriteString(w, `{"instance_id":"inst_cli","operation_id":"18446744073709551615"}`)
				case base + "/operations/18446744073709551615":
					polls++
					if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer access-usr_cli" {
						t.Error("invalid observation request")
					}
					body := map[string]string{"instance_id": "inst_cli", "operation_id": "18446744073709551615", "kind": "CREATE_BRANCH", "state": "success", "branch_id": "child", "parent_branch_id": "main"}
					switch tc.mode {
					case "custom-parent":
						body["parent_branch_id"] = "source"
					case "unavailable":
						w.WriteHeader(503)
						return
					case "malformed":
						io.WriteString(w, `{`)
						return
					case "progress":
						body["state"] = []string{"pending", "running", "retry_wait", "success"}[min(polls-1, 3)]
					case "failed", "unknown":
						body["state"] = tc.mode
					case "missing":
						http.NotFound(w, r)
						return
					case "operation":
						body["operation_id"] = "2"
					case "instance":
						body["instance_id"] = "other"
					case "kind":
						body["kind"] = "DELETE_BRANCH"
					case "parent":
						body["parent_branch_id"] = "other"
					case "child":
						body["branch_id"] = ""
					case "cancel":
						cancel()
						return
					}
					body["message"] = "PRIVATE_OPERATION_DETAILS"
					json.NewEncoder(w).Encode(body)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
			args := []string{"sqlite", "branch", "create", "inst_cli", "preview"}
			if tc.mode == "custom-parent" {
				args = append(args, "--parent", "dev")
			}
			if tc.flag != "" {
				args = append(args, tc.flag)
			}
			var out, diag bytes.Buffer
			code := runCLI(ctx, args, strings.NewReader(""), &out, &diag)
			if code != tc.code || writes != 1 || polls != tc.polls {
				t.Fatalf("code=%d writes=%d polls=%d out=%s diag=%s", code, writes, polls, &out, &diag)
			}
			if !strings.Contains(out.String(), "operation=18446744073709551615") {
				t.Fatal("receipt missing")
			}
			wantSuccess := tc.code == 0 && tc.polls > 0
			if strings.Contains(out.String(), "Branch creation succeeded") != wantSuccess {
				t.Fatalf("incorrect success output %s", &out)
			}
			if strings.Contains(out.String()+diag.String(), "PRIVATE_OPERATION_DETAILS") {
				t.Fatal("peer details exposed")
			}
		})
	}
}

type pendingBranchObserver struct{ calls int }

func (o *pendingBranchObserver) GetInstanceOperation(ctx context.Context, instanceID, operationID string) (authclient.InstanceOperation, error) {
	o.calls++
	return authclient.InstanceOperation{InstanceID: instanceID, OperationID: operationID, Kind: "CREATE_BRANCH", ParentBranchID: "main", State: "pending"}, nil
}

func TestBranchWaitCancelsTimer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	observer := &pendingBranchObserver{}
	start := time.Now()
	err := waitForBranchCreation(ctx, observer, authclient.BranchOperationReceipt{InstanceID: "inst_cli", OperationID: "1"}, "main", time.Hour)
	if !errors.Is(err, context.DeadlineExceeded) || observer.calls != 1 || time.Since(start) > time.Second {
		t.Fatalf("err=%v calls=%d elapsed=%s", err, observer.calls, time.Since(start))
	}
}
