package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

const deletionTestID = "AAAAAAAAAAAA"

func TestWebDeleteRequiresForceAndPreservesUnknownTarget(t *testing.T) {
	gets, deletes := 0, 0
	client := webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/web-projects/"+deletionTestID {
			t.Error("unexpected request path")
		}
		switch r.Method {
		case "GET":
			gets++
			io.WriteString(w, `{"id":"`+deletionTestID+`","name":"Web","owner_id":"owner","tenant_id":"tenant","current_version_id":"v1"}`)
		case "DELETE":
			deletes++
			p, e := authclient.NewFilePendingCommandStore(os.Getenv("TIANA_PENDING_COMMAND_FILE")).Load()
			if e != nil || p.Command != "web.delete" || p.InstanceID != deletionTestID {
				t.Error("delete sent before durable target", e)
			}
			var body struct {
				ExpectedVersionID string `json:"expected_version_id"`
				DeleteGit         bool   `json:"delete_git"`
				DeleteSQLite      bool   `json:"delete_sqlite"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ExpectedVersionID != "v1" || body.DeleteGit || body.DeleteSQLite {
				t.Errorf("delete confirmation=%+v err=%v", body, err)
			}
			if deletes == 1 {
				w.WriteHeader(503)
				return
			}
			io.WriteString(w, `{"id":"`+deletionTestID+`","state":"deleted","requested_at":1,"deleted_at":2}`)
		default:
			t.Error("unexpected method")
		}
	})
	var out, diagnostics bytes.Buffer
	run := func(args []string, output io.Writer) int {
		return runCLI(context.Background(), args, nil, output, &diagnostics)
	}
	if code := run([]string{"web", "delete", deletionTestID, "--json"}, &out); code != 2 || gets != 0 || deletes != 0 {
		t.Fatalf("unconfirmed delete code=%d gets=%d writes=%d", code, gets, deletes)
	}
	out.Reset()
	args := []string{"web", "delete", deletionTestID, "--force", "--json"}
	if code := run(args, &out); code != 4 || deletes != 1 {
		t.Fatalf("unknown delete=%d %s", code, &out)
	}
	original, e := client.LoadCredential()
	if e != nil {
		t.Fatal(e)
	}
	credentialStore := authclient.NewFileStore(os.Getenv("TIANA_CREDENTIALS_FILE"), client.Origin())
	other := original
	other.User.ID = "another-owner"
	if e = credentialStore.Save(other); e != nil {
		t.Fatal(e)
	}
	if code := run(args, io.Discard); code == 0 || deletes != 1 {
		t.Fatal("pending deletion crossed accounts")
	}
	if e = credentialStore.Save(original); e != nil {
		t.Fatal(e)
	}
	if code := run([]string{"web", "delete", "another", "--force"}, io.Discard); code == 0 || deletes != 1 {
		t.Fatal("replaced pending deletion target")
	}
	if code := run(args, rejectedWebOutput{}); code != 1 {
		t.Fatalf("output error=%d", code)
	}
	if _, e := authclient.NewFilePendingCommandStore(os.Getenv("TIANA_PENDING_COMMAND_FILE")).Load(); e != nil {
		t.Fatal("lost receipt on output failure", e)
	}
	out.Reset()
	if code := run(args, &out); code != 0 || gets != 1 || deletes != 3 {
		t.Fatalf("recovery code=%d gets=%d deletes=%d out=%s err=%s", code, gets, deletes, &out, &diagnostics)
	}
	if _, e := authclient.NewFilePendingCommandStore(os.Getenv("TIANA_PENDING_COMMAND_FILE")).Load(); !errors.Is(e, authclient.ErrPendingNotFound) {
		t.Fatal("successful receipt did not clear pending intent", e)
	}
}
func TestWebDeleteWaitCancelLeavesRecoverableIntent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			io.WriteString(w, `{"id":"`+deletionTestID+`","name":"Web","owner_id":"owner","tenant_id":"tenant"}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": deletionTestID, "state": "deleting", "requested_at": 1})
		time.AfterFunc(20*time.Millisecond, cancel)
	})
	var out, diagnostics bytes.Buffer
	code := runCLI(ctx, []string{"web", "delete", deletionTestID, "-f", "-w", "--json"}, nil, &out, &diagnostics)
	if code != 130 {
		t.Fatalf("wait cancellation=%d %s %s", code, &out, &diagnostics)
	}
	p, e := authclient.NewFilePendingCommandStore(os.Getenv("TIANA_PENDING_COMMAND_FILE")).Load()
	if e != nil || p.InstanceID != deletionTestID {
		t.Fatal("cancelled waiting lost server deletion target", e)
	}
}
func TestWebDeleteWaitPollsOnlyOriginalID(t *testing.T) {
	polls, writes := 0, 0
	webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			writes++
			io.WriteString(w, `{"id":"`+deletionTestID+`","state":"deleting","requested_at":1}`)
			return
		}
		if r.URL.Path == "/api/v1/web-projects/"+deletionTestID+"/deletion" {
			polls++
			io.WriteString(w, `{"id":"`+deletionTestID+`","state":"deleted","requested_at":1,"deleted_at":2}`)
			return
		}
		io.WriteString(w, `{"id":"`+deletionTestID+`","name":"Web","owner_id":"owner","tenant_id":"tenant"}`)
	})
	var out, diagnostics bytes.Buffer
	if code := runCLI(t.Context(), []string{"web", "delete", deletionTestID, "-f", "-w", "--json"}, nil, &out, &diagnostics); code != 0 || polls != 1 || writes != 1 {
		t.Fatalf("wait=%d polls=%d writes=%d %s", code, polls, writes, &out)
	}
}

func TestWebDeleteRejectsPendingCreationWithoutRequest(t *testing.T) {
	calls := 0
	client := webManagementFixture(t, func(http.ResponseWriter, *http.Request) { calls++ })
	store := authclient.NewFilePendingCommandStore(os.Getenv("TIANA_PENDING_COMMAND_FILE"))
	pending := authclient.PendingCommand{Command: "web.create", Args: []string{"create", "Web"}, Origin: client.Origin(), UserID: "owner", TenantID: "tenant", IdempotencyKey: "create-request", CreatedAt: time.Now()}
	if err := store.Save(pending); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	code := runCLI(t.Context(), []string{"web", "delete", deletionTestID, "-f", "--json"}, nil, &out, &diagnostics)
	if code != 1 || calls != 0 || !bytes.Contains(out.Bytes(), []byte("WEB_CREATE_IN_PROGRESS")) {
		t.Fatalf("code=%d calls=%d out=%s", code, calls, &out)
	}
	after, err := store.Load()
	if err != nil || after.Command != pending.Command || after.IdempotencyKey != pending.IdempotencyKey {
		t.Fatal("creation intent changed", err)
	}
}

func TestWebDeletePreviewConflictClearsIntentWithoutWaiting(t *testing.T) {
	deletes := 0
	webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			io.WriteString(w, `{"id":"`+deletionTestID+`","name":"Web","owner_id":"owner","tenant_id":"tenant"}`)
			return
		}
		deletes++
		w.WriteHeader(http.StatusConflict)
		io.WriteString(w, `{"error":{"code":"WEB_DELETE_PREVIEW_CHANGED","message":"published version changed"}}`)
	})
	store := authclient.NewFilePendingCommandStore(os.Getenv("TIANA_PENDING_COMMAND_FILE"))
	for i := 0; i < 2; i++ {
		var out, diagnostics bytes.Buffer
		code := runCLI(t.Context(), []string{"web", "delete", deletionTestID, "-f", "-w", "--json"}, nil, &out, &diagnostics)
		if code != 1 || deletes != i+1 || !bytes.Contains(out.Bytes(), []byte("WEB_DELETE_PREVIEW_CHANGED")) {
			t.Fatalf("code=%d deletes=%d out=%s", code, deletes, &out)
		}
		if _, err := store.Load(); !errors.Is(err, authclient.ErrPendingNotFound) {
			t.Fatal("rejected deletion retained intent", err)
		}
	}
}

func TestWebDeleteResolvesPublishedVersionAfterNameLookup(t *testing.T) {
	var version string
	webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/web-projects":
			io.WriteString(w, `{"items":[{"id":"`+deletionTestID+`","name":"My Web","owner_id":"owner","tenant_id":"tenant"}]}`)
		case r.Method == "GET":
			io.WriteString(w, `{"id":"`+deletionTestID+`","name":"My Web","owner_id":"owner","tenant_id":"tenant","current_version_id":"published-v2"}`)
		case r.Method == "DELETE":
			var body struct {
				ExpectedVersionID string `json:"expected_version_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			version = body.ExpectedVersionID
			io.WriteString(w, `{"id":"`+deletionTestID+`","state":"deleting","requested_at":1,"expected_version_id":"published-v2"}`)
		}
	})
	var out, diagnostics bytes.Buffer
	if code := runCLI(t.Context(), []string{"web", "delete", "My Web", "--force", "--json"}, nil, &out, &diagnostics); code != 0 || version != "published-v2" {
		t.Fatalf("code=%d version=%q out=%s diagnostics=%s", code, version, &out, &diagnostics)
	}
}

func TestWebDeleteEmptyApplication(t *testing.T) {
	webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			io.WriteString(w, `{"id":"`+deletionTestID+`","name":"Empty","owner_id":"owner","tenant_id":"tenant","versions":[]}`)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["expected_version_id"] != "" || body["delete_git"] != false || body["delete_sqlite"] != false {
			t.Errorf("confirmation=%v", body)
		}
		io.WriteString(w, `{"id":"`+deletionTestID+`","state":"deleting","requested_at":1,"expected_version_id":""}`)
	})
	var out, diagnostics bytes.Buffer
	if code := runCLI(t.Context(), []string{"web", "delete", deletionTestID, "--force", "--json"}, nil, &out, &diagnostics); code != 0 {
		t.Fatalf("code=%d out=%s diagnostics=%s", code, &out, &diagnostics)
	}
}

func TestWebDeleteRecoversAlreadyAcceptedSelection(t *testing.T) {
	webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "DELETE":
			w.WriteHeader(http.StatusConflict)
			io.WriteString(w, `{"error":{"code":"WEB_DELETE_SELECTION_CONFLICT","message":"read existing deletion"}}`)
		case r.URL.Path == "/api/v1/web-projects/"+deletionTestID+"/deletion":
			io.WriteString(w, `{"id":"`+deletionTestID+`","state":"deleting","requested_at":1,"expected_version_id":"v1","git_instance_id":"git-1"}`)
		default:
			io.WriteString(w, `{"id":"`+deletionTestID+`","name":"Web","owner_id":"owner","tenant_id":"tenant","current_version_id":"v1"}`)
		}
	})
	var out, diagnostics bytes.Buffer
	code := runCLI(t.Context(), []string{"web", "delete", deletionTestID, "--force", "--json"}, nil, &out, &diagnostics)
	if code != 0 || !bytes.Contains(out.Bytes(), []byte(`"git_instance_id":"git-1"`)) {
		t.Fatalf("code=%d output=%s diagnostics=%s", code, &out, &diagnostics)
	}
	if _, err := authclient.NewFilePendingCommandStore(os.Getenv("TIANA_PENDING_COMMAND_FILE")).Load(); !errors.Is(err, authclient.ErrPendingNotFound) {
		t.Fatalf("accepted receipt left pending: %v", err)
	}
}
