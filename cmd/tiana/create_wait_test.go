package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

func TestCreateWaitFlagsAndResume(t *testing.T) {
	for _, product := range []string{"git", "sqlite"} {
		for _, mode := range []string{"long", "short", "cancel-resume", "failure", "async"} {
			t.Run(product+"/"+mode, func(t *testing.T) {
				posts, polls, details := 0, 0, 0
				requestID := ""
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/api/v1/app-types":
						io.WriteString(w, appTypesResponse(product))
					case "/api/v1/instances":
						posts++
						var body authclient.CreateInstanceRequest
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
							return
						}
						requestID = body.RequestID
						w.WriteHeader(202)
						io.WriteString(w, `{"instance_id":"inst_cli","job_id":17}`)
					case "/api/v1/jobs/17":
						polls++
						if mode == "async" {
							t.Error("async create polled")
						}
						if mode == "cancel-resume" && polls == 1 {
							cancel()
							return
						}
						if mode == "failure" {
							fmt.Fprintf(w, `{"job_id":17,"instance_id":"inst_cli","job_kind":"instance_create","request_id":%q,"status":"fail"}`, requestID)
							return
						}
						if polls == 1 {
							fmt.Fprintf(w, `{"job_id":17,"instance_id":"inst_cli","job_kind":"instance_create","request_id":%q,"status":"running"}`, requestID)
							return
						}
						w.WriteHeader(404)
						io.WriteString(w, `{"error":{"code":"NOT_FOUND"}}`)
					case "/api/v1/instances/inst_cli":
						details++
						fmt.Fprintf(w, `{"id":"inst_cli","engine":%q,"product_state":"ACTIVE"}`, product)
					default:
						t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				env := newTestEnv(t, server.URL)
				saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
				args := []string{product, "create", "demo", "--wait"}
				if mode == "short" {
					args = []string{product, "create", "-w", "demo"}
				}
				if mode == "async" {
					args = []string{product, "create", "demo", "--wait=false"}
				}
				var out, diag bytes.Buffer
				code := runCLI(ctx, args, strings.NewReader(""), &out, &diag)
				store := authclient.NewFilePendingCommandStore(env.pendingPath)
				if mode == "failure" || mode == "cancel-resume" {
					want := 1
					if mode == "cancel-resume" {
						want = 130
					}
					if code != want {
						t.Fatalf("code=%d diag=%s", code, &diag)
					}
					p, err := store.Load()
					if err != nil || p.CreationJobID != 17 || !sameStrings(p.Args, []string{"create", "demo"}) {
						t.Fatalf("lost stable receipt: %v", err)
					}
					if strings.Contains(out.String(), "Creation succeeded") {
						t.Fatal("false success")
					}
					if mode == "failure" {
						if posts != 1 {
							t.Fatal("creation repeated")
						}
						return
					}
					out.Reset()
					diag.Reset()
					code = runCLI(context.Background(), []string{product, "create", "demo", "-w"}, strings.NewReader(""), &out, &diag)
				}
				if code != 0 || posts != 1 {
					t.Fatalf("code=%d posts=%d diag=%s", code, posts, &diag)
				}
				if mode != "async" && (polls != 2 || details != 1 || !strings.Contains(out.String(), "Creation succeeded")) {
					t.Fatalf("polls=%d details=%d output=%s", polls, details, &out)
				}
				if _, err := store.Load(); err != authclient.ErrPendingNotFound {
					t.Fatalf("receipt retained: %v", err)
				}
			})
		}
	}
}

type creationObservation struct {
	job          authclient.Job
	jobErr       error
	instance     authclient.Instance
	instanceErr  error
	polls, reads int
	cancel       context.CancelFunc
}

func (o *creationObservation) GetJob(context.Context, uint64) (authclient.Job, error) {
	o.polls++
	if o.cancel != nil {
		o.cancel()
	}
	return o.job, o.jobErr
}
func (o *creationObservation) GetInstance(context.Context, string) (authclient.Instance, error) {
	o.reads++
	return o.instance, o.instanceErr
}

func TestCreateWaitVerifiesTerminalResult(t *testing.T) {
	for _, mode := range []string{"complete", "removed", "failed", "wrong-job", "wrong-instance", "wrong-request", "wrong-kind", "unknown-job-status", "unavailable", "missing-instance", "failed-instance", "wrong-result", "wrong-engine", "deleted", "deleting", "unknown-instance", "no-job-active", "no-job-pending", "cancel-timer"} {
		t.Run(mode, func(t *testing.T) {
			pending := authclient.PendingCommand{InstanceID: "inst_cli", CreationJobID: 17, IdempotencyKey: "request"}
			o := &creationObservation{job: authclient.Job{JobID: 17, InstanceID: "inst_cli", RequestID: "request", JobKind: "instance_create", Status: "complete"}, instance: authclient.Instance{ID: "inst_cli", Engine: "git", ProductState: "ACTIVE"}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "removed":
				o.jobErr = &authclient.APIError{Status: 404}
			case "failed":
				o.job.Status = "fail"
			case "wrong-job":
				o.job.JobID = 18
			case "wrong-instance":
				o.job.InstanceID = "other"
			case "wrong-request":
				o.job.RequestID = "other"
			case "wrong-kind":
				o.job.JobKind = "instance_delete"
			case "unknown-job-status":
				o.job.Status = "unknown"
			case "unavailable":
				o.jobErr = &authclient.APIError{Status: 503}
			case "missing-instance":
				o.jobErr = &authclient.APIError{Status: 404}
				o.instanceErr = &authclient.APIError{Status: 404}
			case "failed-instance":
				o.instance.ProductState = "FAILED"
			case "wrong-result":
				o.instance.ID = "other"
			case "wrong-engine":
				o.instance.Engine = "sqlite"
			case "deleted":
				o.instance.ProductState = "DELETED"
			case "deleting":
				o.instance.DeletionPending = true
			case "unknown-instance":
				o.instance.ProductState = "invalid"
			case "no-job-active":
				pending.CreationJobID = 0
			case "no-job-pending":
				pending.CreationJobID = 0
				o.instance.ProductState = "PENDING"
			case "cancel-timer":
				o.job.Status = "running"
				o.cancel = cancel
			}
			err := waitForInstanceCreation(ctx, o, pending, gitManagementScope, time.Hour)
			success := mode == "complete" || mode == "removed" || mode == "no-job-active"
			if (err == nil) != success {
				t.Fatalf("mode=%s err=%v", mode, err)
			}
			if mode == "cancel-timer" && !errors.Is(err, context.Canceled) {
				t.Fatalf("err=%v", err)
			}
			if mode == "failed" && o.reads != 0 {
				t.Fatal("failed job treated as complete")
			}
		})
	}
}

func TestCreateWaitArgumentIdentity(t *testing.T) {
	for _, tc := range []struct{ args, want []string }{
		{[]string{"-w", "demo"}, []string{"demo"}},
		{[]string{"demo", "--wait=true"}, []string{"demo"}},
		{[]string{"--wait=false", "demo"}, []string{"demo"}},
		{[]string{"-w=false", "demo"}, []string{"demo"}},
		{[]string{"--wait", "--", "-w"}, []string{"--", "-w"}},
		{[]string{"--", "--wait=true"}, []string{"--", "--wait=true"}},
	} {
		if got := createIdentityArguments(tc.args); !sameStrings(got, tc.want) {
			t.Fatalf("got=%v want=%v", got, tc.want)
		}
	}
}
