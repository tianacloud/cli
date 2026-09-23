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

func TestDeleteWait(t *testing.T) {
	for _, target := range []string{"git", "sqlite", "branch"} {
		for _, tc := range []struct {
			name, flag, mode string
			code, polls      int
		}{
			{"default", "", "success", 0, 0},
			{"false", "--wait=false", "success", 0, 0},
			{"short", "-w", "success", 0, 1},
			{"long", "--wait", "success", 0, 1},
			{"failed", "-w", "failed", 1, 1},
			{"unknown", "-w", "unknown", 1, 1},
			{"missing", "-w", "missing", 1, 1},
			{"unavailable", "-w", "unavailable", 1, 1},
			{"wrong instance", "-w", "instance", 1, 1},
			{"wrong operation", "-w", "operation", 1, 1},
			{"wrong kind", "-w", "kind", 1, 1},
			{"wrong branch", "-w", "branch", 1, 1},
			{"malformed", "-w", "malformed", 1, 1},
			{"cancel", "-w", "cancel", 130, 1},
			{"receipt write failure", "-w", "output", 1, 0},
			{"confirmation required", "-w", "confirmation", 2, 0},
		} {
			if tc.mode == "branch" && target != "branch" {
				continue
			}
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				writes, polls, reads := 0, 0, 0
				engine, kind := target, "DELETE_INSTANCE"
				if target == "branch" {
					engine = "sqlite"
					kind = "DELETE_BRANCH"
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					const base = "/api/v1/instances/inst_cli"
					if r.Method == "DELETE" {
						writes++
						want := base
						if target == "branch" {
							want += "/branches/child"
						}
						if r.URL.Path != want {
							t.Errorf("wrong deletion path %s", r.URL)
						}
						w.WriteHeader(202)
						io.WriteString(w, `{"instance_id":"inst_cli","operation_id":"18446744073709551615"}`)
						return
					}
					if r.Method != "GET" {
						t.Errorf("unexpected method %s", r.Method)
					}
					switch r.URL.Path {
					case base:
						reads++
						json.NewEncoder(w).Encode(map[string]string{"id": "inst_cli", "engine": engine})
					case base + "/branches/child":
						reads++
						io.WriteString(w, `{"instance_id":"inst_cli","branch":{"branch_id":"child","name":"preview"}}`)
					case base + "/operations/18446744073709551615":
						polls++
						if r.Header.Get("Authorization") != "Bearer access-usr_cli" {
							t.Error("missing auth")
						}
						body := map[string]string{"instance_id": "inst_cli", "operation_id": "18446744073709551615", "kind": kind, "state": "success", "branch_id": "child"}
						switch tc.mode {
						case "failed", "unknown":
							body["state"] = tc.mode
						case "missing":
							http.NotFound(w, r)
							return
						case "unavailable":
							w.WriteHeader(503)
							return
						case "instance":
							body["instance_id"] = "other"
						case "operation":
							body["operation_id"] = "99"
						case "malformed":
							io.WriteString(w, `{`)
							return
						case "branch":
							body["branch_id"] = "other"
						case "kind":
							body["kind"] = "CREATE_BRANCH"
						case "cancel":
							cancel()
							return
						}
						body["message"] = "PRIVATE_PEER_DETAILS"
						json.NewEncoder(w).Encode(body)
					default:
						t.Errorf("unexpected query %s", r.URL)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				env := newTestEnv(t, server.URL)
				saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
				args := []string{target, "delete", "inst_cli"}
				if target == "branch" {
					args = []string{"sqlite", "branch", "delete", "inst_cli", "child", "--by-id"}
				}
				if tc.mode != "confirmation" {
					args = append(args, "-f")
				}
				if tc.flag != "" {
					args = append(args, tc.flag)
				}
				var out, diag bytes.Buffer
				var output io.Writer = &out
				if tc.mode == "output" {
					output = failedReceiptWriter{}
				}
				code := runCLI(ctx, args, strings.NewReader(""), output, &diag)
				wantWrites := 1
				if tc.mode == "confirmation" {
					wantWrites = 0
					if reads != 0 {
						t.Error("requests before confirmation guard")
					}
				}
				if code != tc.code || writes != wantWrites || polls != tc.polls {
					t.Fatalf("code=%d writes=%d polls=%d out=%s diag=%s", code, writes, polls, &out, &diag)
				}
				success := tc.code == 0 && tc.polls > 0
				if strings.Contains(out.String(), "Deletion succeeded") != success {
					t.Fatalf("false completion: %s", &out)
				}
				if strings.Contains(out.String()+diag.String(), "PRIVATE_PEER_DETAILS") {
					t.Fatal("peer details leaked")
				}
			})
		}
	}
}

type deletionSequenceObserver struct {
	calls  int
	states []string
}

func (o *deletionSequenceObserver) GetInstanceOperation(ctx context.Context, instanceID, operationID string) (authclient.InstanceOperation, error) {
	index := min(o.calls, len(o.states)-1)
	o.calls++
	return authclient.InstanceOperation{InstanceID: instanceID, OperationID: operationID, Kind: "DELETE_BRANCH", BranchID: "child", State: o.states[index]}, nil
}
func TestDeletionWaitProgressAndTimerCancellation(t *testing.T) {
	observer := &deletionSequenceObserver{states: []string{"pending", "running", "retry_wait", "success"}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForDeletion(ctx, observer, "inst_cli", "17", "DELETE_BRANCH", "child", time.Millisecond); err != nil || observer.calls != 4 {
		t.Fatalf("err=%v calls=%d", err, observer.calls)
	}
	observer = &deletionSequenceObserver{states: []string{"pending"}}
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := waitForDeletion(ctx, observer, "inst_cli", "17", "DELETE_BRANCH", "child", time.Hour); !errors.Is(err, context.DeadlineExceeded) || observer.calls != 1 || time.Since(start) > time.Second {
		t.Fatalf("err=%v calls=%d elapsed=%s", err, observer.calls, time.Since(start))
	}
}
