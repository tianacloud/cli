package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/tianacloud/cli/internal/testutil/account"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

type rejectedWebOutput struct{}

func (rejectedWebOutput) Write([]byte) (int, error) { return 0, errors.New("output interrupted") }

func TestWebCreatePreservesRequestAcrossUnknownResultAndOutputFailure(t *testing.T) {
	dir := t.TempDir()
	pendingPath := filepath.Join(dir, "pending.json")
	t.Setenv("TIANA_PENDING_COMMAND_FILE", pendingPath)
	var mu sync.Mutex
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/web-projects" {
			t.Error("unexpected creation route")
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body) != 3 || body["name"] != "Web" || body["description"] != "应用描述" || body["request_id"] == "" {
			t.Errorf("body=%v", body)
		}
		pending, err := authclient.NewFilePendingCommandStore(pendingPath).Load()
		if err != nil || pending.IdempotencyKey != body["request_id"] {
			t.Errorf("request sent before durable intent: %v", err)
		}
		mu.Lock()
		keys = append(keys, body["request_id"])
		n := len(keys)
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(503)
			io.WriteString(w, `{"error":{"code":"UNAVAILABLE","message":"response lost"}}`)
			return
		}
		io.WriteString(w, `{"id":"AAAAAAAAAAAA","name":"Web","description":"应用描述","owner_id":"owner","tenant_id":"tenant"}`)
	}))
	defer server.Close()
	t.Setenv("TIANA_API_ORIGIN", server.URL)
	creds := account.CredentialPath(t)
	if err := authclient.NewFileStore(creds, server.URL).Save(authclient.Credential{AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour), User: authclient.User{ID: "owner", TenantID: "tenant"}}); err != nil {
		t.Fatal(err)
	}
	run := func(name string, out io.Writer) int {
		var diagnostics bytes.Buffer
		return runCLI(context.Background(), []string{"web", "create", name, "-m", "应用描述", "--json"}, nil, out, &diagnostics)
	}
	var out bytes.Buffer
	if code := run("Web", &out); code != 4 {
		t.Fatalf("unknown status=%d %s", code, &out)
	}
	store := authclient.NewFilePendingCommandStore(pendingPath)
	original, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if code := runCLI(t.Context(), []string{"web", "create", "Web", "-m", "changed", "--json"}, nil, io.Discard, io.Discard); code == 0 {
		t.Fatal("overwrote pending description")
	}
	if code := run("Different", io.Discard); code == 0 {
		t.Fatal("overwrote pending name")
	}
	if code := run("Web", rejectedWebOutput{}); code != 1 {
		t.Fatalf("output error status=%d", code)
	}
	saved, err := store.Load()
	if err != nil || saved.IdempotencyKey != original.IdempotencyKey {
		t.Fatalf("output failure lost request: %+v %v", saved, err)
	}
	out.Reset()
	if code := run("Web", &out); code != 0 {
		t.Fatalf("recovery status=%d %s", code, &out)
	}
	if _, err = store.Load(); !errors.Is(err, authclient.ErrPendingNotFound) {
		t.Fatalf("successful output did not clear intent: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(keys) != 3 {
		t.Fatalf("requests=%d", len(keys))
	}
	for _, key := range keys {
		if key != original.IdempotencyKey {
			t.Fatal("retry minted a new request identity")
		}
	}
}

func TestWebCreateRejectsCallerSelectedIdentity(t *testing.T) {
	t.Setenv("TIANA_PENDING_COMMAND_FILE", filepath.Join(t.TempDir(), "pending.json"))
	for _, args := range [][]string{
		{"web", "create"}, {"web", "create", "Web", "Extra"},
		{"web", "create", "--project", "chosen", "--name", "Web"},
		{"web", "create", "Web", "--id", "chosen"}, {"web", "create", "Web", "--name", "Other"},
	} {
		var out, diagnostics bytes.Buffer
		if code := runCLI(t.Context(), args, nil, &out, &diagnostics); code != 2 {
			t.Errorf("%v status=%d %s %s", args, code, &out, &diagnostics)
		}
	}
}

func TestWebCreateDescriptionFlagsAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name        string
		args        []string
		description string
		valid       bool
	}{
		{"omitted", nil, "", true},
		{"short", []string{"-m", "应用描述\n第二行"}, "应用描述\n第二行", true},
		{"long", []string{"--description", "summary"}, "summary", true},
		{"boundary", []string{"-m", strings.Repeat("界", 682) + "ab"}, strings.Repeat("界", 682) + "ab", true},
		{"too-long", []string{"-m", strings.Repeat("界", 683)}, "", false},
		{"invalid-utf8", []string{"-m", string([]byte{0xff})}, "", false},
		{"control", []string{"-m", "bad\x1b[31m"}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["description"] != tc.description {
					t.Errorf("description=%q", body["description"])
				}
				json.NewEncoder(w).Encode(map[string]string{"id": deletionTestID, "name": "Web", "description": body["description"], "owner_id": "owner", "tenant_id": "tenant"})
			})
			args := append([]string{"web", "create", "Web", "--json"}, tc.args...)
			var out, diagnostics bytes.Buffer
			code := runCLI(t.Context(), args, nil, &out, &diagnostics)
			if tc.valid && (code != 0 || calls != 1) || !tc.valid && (code != 2 || calls != 0) {
				t.Fatalf("code=%d calls=%d out=%s err=%s", code, calls, &out, &diagnostics)
			}
		})
	}
}

func TestWebCreateMismatchedDescriptionRetainsIntent(t *testing.T) {
	webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":"`+deletionTestID+`","name":"Web","description":"wrong","owner_id":"owner","tenant_id":"tenant"}`)
	})
	var out, diagnostics bytes.Buffer
	code := runCLI(t.Context(), []string{"web", "create", "Web", "-m", "expected", "--json"}, nil, &out, &diagnostics)
	if code != 4 || !bytes.Contains(out.Bytes(), []byte("CREATE_OUTCOME_UNKNOWN")) {
		t.Fatalf("code=%d out=%s", code, &out)
	}
	pending, err := authclient.NewFilePendingCommandStore(os.Getenv("TIANA_PENDING_COMMAND_FILE")).Load()
	if err != nil || !sameStrings(pending.Args, []string{"create", "Web", "--description", "expected"}) {
		t.Fatal("lost exact pending description", err)
	}
}
