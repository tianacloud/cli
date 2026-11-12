package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWebShareSettingsPreserveRevisionAndExplicitFalse(t *testing.T) {
	revision := uint64(9007199254740993)
	enabled, automatic, changes := false, false, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/web-projects/abcdefghijkl/share" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if r.Method == "POST" {
			var body struct {
				Enabled  *bool  `json:"share_enabled"`
				Auto     *bool  `json:"share_auto_approve"`
				Revision uint64 `json:"expected_revision"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Revision != revision || (body.Enabled == nil) == (body.Auto == nil) {
				t.Errorf("invalid settings update %+v", body)
				w.WriteHeader(400)
				return
			}
			if body.Enabled != nil {
				enabled = *body.Enabled
			}
			if body.Auto != nil {
				automatic = *body.Auto
			}
			revision++
			changes++
		}
		json.NewEncoder(w).Encode(map[string]any{"web_id": "abcdefghijkl", "project_revision": revision, "share_enabled": enabled, "share_auto_approve": automatic, "feature_enabled": true, "can_manage": true, "can_approve": true, "url": "https://site.example.test/invitation", "limits": map[string]any{"effective": 100}})
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_share")
	for _, flags := range [][]string{
		{"get"},
		{"set", "--enabled=true", "--expected-revision", "9007199254740993"},
		{"set", "--auto-approve=true", "--expected-revision", "9007199254740994"},
		{"set", "--enabled=false", "--expected-revision", "9007199254740995"},
		{"set", "--auto-approve=false", "--expected-revision", "9007199254740996"},
	} {
		args := append([]string{"web", "share", flags[0], "abcdefghijkl"}, flags[1:]...)
		args = append(args, "--json")
		var out, diag bytes.Buffer
		code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diag)
		if code != 0 || !strings.Contains(out.String(), `"status":"succeeded"`) {
			t.Fatalf("%v: %d %s %s", args, code, out.String(), diag.String())
		}
	}
	if changes != 4 || enabled || automatic || revision != 9007199254740997 {
		t.Fatalf("final settings %v %v %d %d", enabled, automatic, changes, revision)
	}
}

func TestWebShareRejectsUnspecifiedOrCombinedSettings(t *testing.T) {
	for _, flags := range [][]string{
		{"--expected-revision", "1"},
		{"--enabled=true"},
		{"--enabled=true", "--auto-approve=true", "--expected-revision", "1"},
	} {
		var out, diag bytes.Buffer
		args := append([]string{"web", "share", "set", "abcdefghijkl"}, flags...)
		if runCLI(context.Background(), args, strings.NewReader(""), &out, &diag) == 0 {
			t.Fatalf("accepted invalid flags %v", flags)
		}
	}
}

func TestWebShareLostResponseRetainsTargetWithoutReplay(t *testing.T) {
	var changes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		changes.Add(1)
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		connection.Close()
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_share")
	var out, diag bytes.Buffer
	code := runCLI(context.Background(), []string{"web", "share", "set", "abcdefghijkl", "--enabled=true", "--expected-revision", "7", "--json"}, strings.NewReader(""), &out, &diag)
	if code != 4 || changes.Load() != 1 || !strings.Contains(out.String(), `"status":"unknown"`) || !strings.Contains(out.String(), `"web_id":"abcdefghijkl"`) || !strings.Contains(out.String(), `"expected_revision":7`) {
		t.Fatalf("lost response: %d %d %s %s", code, changes.Load(), out.String(), diag.String())
	}
}
