package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

func runSpaceJSON(t *testing.T, args ...string) (int, map[string]any, string) {
	t.Helper()
	var out, diag bytes.Buffer
	code := runCLI(t.Context(), append(args, "--json"), strings.NewReader(""), &out, &diag)
	return code, decodeManagementJSON(t, &out), diag.String()
}

func TestSpaceLifecycleUsesExistingCredentials(t *testing.T) {
	var createdKey, renamed string
	var requests []string
	client := webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.URL.Path != "/api/v1/tenant" && r.Header.Get("Authorization") != "Bearer access" {
			t.Error("unexpected account token")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/me/tenants":
			if r.URL.Query().Get("after") == "" {
				io.WriteString(w, `{"items":[{"tenant_id":"tenant","display_name":"Home","role":"tenant_owner","is_default":true}],"next_cursor":"tenant"}`)
			} else {
				io.WriteString(w, `{"items":[{"tenant_id":"other","display_name":"Shared","role":"tenant_operator"}],"next_cursor":""}`)
			}
		case "POST /api/v1/me/tenants":
			createdKey = r.Header.Get("Idempotency-Key")
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["display_name"] != "New space" {
				t.Error("creation name changed")
			}
			io.WriteString(w, `{"tenant_id":"created","display_name":"New space","role":"tenant_owner","revision":"1"}`)
		case "GET /api/v1/tenant":
			if r.Header.Get("Authorization") == "Bearer new-private-access" {
				io.WriteString(w, `{"tenant_id":"other","display_name":"Shared","role":"tenant_operator","revision":"2"}`)
			} else {
				io.WriteString(w, `{"tenant_id":"tenant","display_name":"Home","role":"tenant_owner","revision":"9007199254740993","can_manage":true}`)
			}
		case "PATCH /api/v1/tenant":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["expected_revision"] != "9007199254740993" {
				t.Error("revision lost precision")
			}
			renamed = body["display_name"]
			io.WriteString(w, `{"tenant_id":"tenant","display_name":"Renamed","revision":"9007199254740994"}`)
		case "POST /api/v1/me/tenant":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["tenant_id"] != "other" {
				t.Error("wrong switch target")
			}
			io.WriteString(w, `{"access_token":"new-private-access","refresh_token":"new-private-refresh","token_type":"Bearer","expires_in":3600,"user":{"user_id":"owner","tenant_id":"other"},"sync_status":"pending","job_id":9007199254740993,"extra":true}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	})
	path, _ := authclient.DefaultCredentialPath()
	beforeBytes, _ := os.ReadFile(path)
	var before map[string]json.RawMessage
	json.Unmarshal(beforeBytes, &before)
	code, result, diag := runSpaceJSON(t, "space", "list")
	data := result["data"].(map[string]any)
	if code != 0 || len(data["items"].([]any)) != 2 || data["current_tenant_id"] != "tenant" {
		t.Fatalf("list: %d %v %s", code, result, diag)
	}
	code, result, diag = runSpaceJSON(t, "space", "create", "New space", "--request-id", "create-key")
	if code != 0 || createdKey != "create-key" {
		t.Fatalf("create: %d %v %s", code, result, diag)
	}
	credential, _ := client.LoadCredential()
	if credential.User.TenantID != "tenant" {
		t.Fatal("creation switched space")
	}
	code, result, diag = runSpaceJSON(t, "space", "rename", "Renamed")
	if code != 0 || renamed != "Renamed" {
		t.Fatalf("rename: %d %v %s", code, result, diag)
	}
	code, result, diag = runSpaceJSON(t, "space", "switch", "other")
	data = result["data"].(map[string]any)
	if code != 0 || result["status"] != "succeeded" || data["sync_status"] != "pending" || data["job_id"] != "9007199254740993" {
		t.Fatalf("switch: %d %v %s", code, result, diag)
	}
	printed, _ := json.Marshal(result)
	if strings.Contains(string(printed)+diag, "new-private") {
		t.Fatal("switch exposed credentials")
	}
	credential, err := client.LoadCredential()
	if err != nil || credential.User.TenantID != "other" || credential.AccessToken != "new-private-access" || credential.RefreshToken != "new-private-refresh" {
		t.Fatal("new credential was not persisted")
	}
	afterBytes, _ := os.ReadFile(path)
	var after map[string]json.RawMessage
	json.Unmarshal(afterBytes, &after)
	if len(before) != len(after) {
		t.Fatal("credential file structure changed")
	}
	for key := range before {
		if _, ok := after[key]; !ok {
			t.Fatal("credential file structure changed")
		}
	}
	code, result, diag = runSpaceJSON(t, "space", "show")
	if code != 0 || result["data"].(map[string]any)["tenant_id"] != "other" {
		t.Fatalf("show: %d %v %s", code, result, diag)
	}
	for _, request := range requests {
		if strings.Contains(request, "whoami") {
			t.Fatal("switch waited for sync")
		}
	}
}

func TestSpaceInviteBatchAndAcceptance(t *testing.T) {
	var posted []string
	keys := map[string]bool{}
	client := webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/tenant/invitations":
			io.WriteString(w, `{"items":[{"invitation_id":"inv-existing","tenant_id":"tenant","user_id":"existing","revision":"9007199254740993","state":"expired"}],"next_cursor":""}`)
		case "POST /api/v1/tenant/invitations":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			user := body["user_id"]
			posted = append(posted, user)
			key := r.Header.Get("Idempotency-Key")
			if key == "" || keys[key] {
				t.Error("invitation identity reused")
			}
			keys[key] = true
			wantRevision := "0"
			if user == "existing" {
				wantRevision = "9007199254740993"
			}
			if body["expected_revision"] != wantRevision {
				t.Errorf("wrong revision for %s", user)
			}
			if user == "missing" {
				w.WriteHeader(404)
				io.WriteString(w, `{"error":{"code":"TENANT_RECORD_NOT_FOUND"}}`)
				return
			}
			if user == "uncertain" {
				w.WriteHeader(503)
				io.WriteString(w, `{"error":{"code":"UNAVAILABLE"}}`)
				return
			}
			fmt.Fprintf(w, `{"invitation_id":"inv-%s","tenant_id":"tenant","user_id":%q,"revision":"9007199254740994","state":"pending"}`, user, user)
		case "POST /api/v1/invitations/inv-received/preview":
			body, _ := io.ReadAll(r.Body)
			if string(body) != "{}" {
				t.Error("preview needs an empty object")
			}
			io.WriteString(w, `{"invitation_id":"inv-received","tenant_id":"joined","user_id":"owner","tenant_name":"Joined space","state":"ready"}`)
		case "POST /api/v1/invitations/inv-received/accept":
			body, _ := io.ReadAll(r.Body)
			if string(body) != "{}" {
				t.Error("accept needs an empty object")
			}
			io.WriteString(w, `{"user_id":"owner","role":"tenant_operator","revision":"1"}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	})
	code, result, diag := runSpaceJSON(t, "space", "invitation", "invite", "existing", "new", "missing", "uncertain", "after", "existing")
	if code != 4 || result["status"] != "unknown" || !reflect.DeepEqual(posted, []string{"existing", "new", "missing", "uncertain", "after"}) {
		t.Fatalf("batch: %d %v %v %s", code, result, posted, diag)
	}
	items := result["data"].(map[string]any)["items"].([]any)
	for i, want := range []string{"succeeded", "succeeded", "failed", "unknown", "succeeded"} {
		if items[i].(map[string]any)["status"] != want {
			t.Fatalf("item %d=%v", i, items[i])
		}
	}
	code, result, diag = runSpaceJSON(t, "space", "invitation", "accept", "inv-received")
	if code != 0 || result["data"].(map[string]any)["tenant_id"] != "joined" {
		t.Fatalf("accept: %d %v %s", code, result, diag)
	}
	credential, _ := client.LoadCredential()
	if credential.User.TenantID != "tenant" {
		t.Fatal("accept switched the current space")
	}
}

func TestSpaceMemberChangesAndLeave(t *testing.T) {
	var revisions []string
	client := webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/tenant/members":
			io.WriteString(w, `{"items":[{"user_id":"member","display_name":"Member","role":"tenant_operator","revision":"7"}],"next_cursor":""}`)
		case "POST /api/v1/tenant/members/member/admin", "DELETE /api/v1/tenant/members/member":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			revisions = append(revisions, body["expected_revision"])
			io.WriteString(w, `{"user_id":"member","role":"tenant_admin","revision":"8"}`)
		case "GET /api/v1/me/tenants":
			io.WriteString(w, `{"items":[{"tenant_id":"tenant","display_name":"Current","role":"tenant_operator"},{"tenant_id":"other","role":"tenant_operator"}]}`)
		case "POST /api/v1/me/tenants/other/leave":
			io.WriteString(w, `{"logged_out":false}`)
		case "POST /api/v1/me/tenants/tenant/leave":
			io.WriteString(w, `{"logged_out":true}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	})
	for _, args := range [][]string{{"space", "member", "set-admin", "member"}, {"space", "member", "remove", "member", "--force"}, {"space", "leave", "other", "--force"}} {
		code, result, diag := runSpaceJSON(t, args...)
		if code != 0 {
			t.Fatalf("%v: %d %v %s", args, code, result, diag)
		}
	}
	if !reflect.DeepEqual(revisions, []string{"7", "7"}) {
		t.Fatal("member revision not used")
	}
	if _, err := client.LoadCredential(); err != nil {
		t.Fatal("leaving another space deleted login")
	}
	code, result, diag := runSpaceJSON(t, "space", "leave", "tenant", "--force")
	if code != 0 || result["data"].(map[string]any)["logged_out"] != true {
		t.Fatalf("leave: %d %v %s", code, result, diag)
	}
	if _, err := client.LoadCredential(); err != authclient.ErrCredentialNotFound {
		t.Fatal("leaving current space retained local credential")
	}
}

func TestSpaceSwitchFailureStages(t *testing.T) {
	for _, mode := range []string{"rejected", "lost", "incomplete", "save-failed"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			client := webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "POST" || r.URL.Path != "/api/v1/me/tenant" {
					t.Errorf("unexpected request %s", r.URL)
				}
				switch mode {
				case "rejected":
					w.WriteHeader(403)
					io.WriteString(w, `{"error":{"code":"TENANT_MANAGEMENT_FORBIDDEN"}}`)
				case "lost":
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					connection.Close()
				case "incomplete":
					io.WriteString(w, `{"access_token":"synthetic-only"}`)
				case "save-failed":
					path, _ := authclient.DefaultCredentialPath()
					if err := os.Remove(path); err != nil {
						t.Error(err)
					}
					if err := os.Mkdir(path, 0700); err != nil {
						t.Error(err)
					}
					io.WriteString(w, `{"access_token":"synthetic-next","refresh_token":"synthetic-refresh","expires_in":3600,"user":{"user_id":"owner","tenant_id":"other"},"sync_status":"pending"}`)
				}
			})
			code, result, diag := runSpaceJSON(t, "space", "switch", "other")
			want := 4
			if mode == "rejected" || mode == "save-failed" {
				want = 1
			}
			if code != want || calls.Load() != 1 {
				t.Fatalf("%s: %d %v calls=%d %s", mode, code, result, calls.Load(), diag)
			}
			if mode == "rejected" {
				credential, err := client.LoadCredential()
				if err != nil || credential.User.TenantID != "tenant" {
					t.Fatal("rejection changed login")
				}
			}
			if mode == "save-failed" && !strings.Contains(result["error"].(map[string]any)["next_action"].(string), "server switched") {
				t.Fatal("save failure does not state the server outcome")
			}
		})
	}
}

func TestSpaceInviteStopsAfterAuthenticationFailure(t *testing.T) {
	writes := 0
	webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			io.WriteString(w, `{"items":[],"next_cursor":""}`)
			return
		}
		writes++
		w.WriteHeader(401)
		io.WriteString(w, `{"error":{"code":"UNAUTHORIZED"}}`)
	})
	code, result, diag := runSpaceJSON(t, "space", "invitation", "invite", "user-a", "user-b")
	if code != 1 || writes != 1 {
		t.Fatalf("code=%d writes=%d result=%v diag=%s", code, writes, result, diag)
	}
	items := result["data"].(map[string]any)["items"].([]any)
	if items[1].(map[string]any)["status"] != "not_executed" {
		t.Fatalf("items=%v", items)
	}
}

func TestSpaceSwitchSavedDespiteOutputFailure(t *testing.T) {
	client := webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600,"user":{"user_id":"owner","tenant_id":"other"},"sync_status":"pending"}`)
	})
	var diag bytes.Buffer
	code := runCLI(t.Context(), []string{"space", "switch", "other", "--json"}, nil, failedReceiptWriter{}, &diag)
	saved, err := client.LoadCredential()
	if code != 1 || err != nil || saved.User.TenantID != "other" || !strings.Contains(diag.String(), "credentials were saved") {
		t.Fatalf("code=%d err=%v diag=%s", code, err, &diag)
	}
}

func TestSpaceSwitchSavesAfterConcurrentRefreshFailure(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	client := webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/me/tenant":
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			io.WriteString(w, `{"access_token":"selected-access","refresh_token":"selected-refresh","expires_in":3600,"user":{"user_id":"owner","tenant_id":"other"},"sync_status":"pending"}`)
		case "/api/v1/auth/transactions/whoami":
			w.WriteHeader(401)
			io.WriteString(w, `{"error":"expired_access"}`)
		case "/api/v1/auth/refresh":
			w.WriteHeader(400)
			io.WriteString(w, `{"error":"invalid_grant"}`)
		default:
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(500)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	completed := make(chan int, 1)
	var out, diag bytes.Buffer
	go func() { completed <- runCLI(ctx, []string{"space", "switch", "other", "--json"}, nil, &out, &diag) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("switch did not start")
	}
	_, refreshErr := client.AccountState(ctx)
	_, loadErr := client.LoadCredential()
	close(release)
	select {
	case code := <-completed:
		if code != 0 {
			t.Fatalf("switch code=%d diag=%s", code, &diag)
		}
	case <-ctx.Done():
		t.Fatal("switch did not finish")
	}
	if !errors.Is(refreshErr, authclient.ErrAuthenticationRequired) || !errors.Is(loadErr, authclient.ErrCredentialNotFound) {
		t.Fatalf("refresh=%v temporary credential load=%v", refreshErr, loadErr)
	}
	credential, err := client.LoadCredential()
	if err != nil || credential.User.TenantID != "other" || credential.AccessToken != "selected-access" {
		t.Fatalf("new login not saved: %v", err)
	}
}
