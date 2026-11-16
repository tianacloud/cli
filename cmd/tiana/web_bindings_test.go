package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWebBindingCommands(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		body     map[string]any
		response string
	}{
		{"read", []string{"get"}, nil, `{"web_id":"orders","database_instance_id":"db-one","git_instance_id":"git-one","project_revision":7,"can_manage":true}`},
		{"bind database", []string{"bind", "--database-instance-id", "db-one", "--expected-revision", "7"}, map[string]any{"database_instance_id": "db-one", "expected_revision": float64(7)}, `{"web_id":"orders","database_instance_id":"db-one","git_instance_id":"git-kept","project_revision":8,"can_manage":true}`},
		{"bind git", []string{"bind", "--git-instance-id", "git-one", "--expected-revision", "7"}, map[string]any{"git_instance_id": "git-one", "expected_revision": float64(7)}, `{"web_id":"orders","database_instance_id":"db-kept","git_instance_id":"git-one","project_revision":8,"can_manage":true}`},
		{"unbind both", []string{"unbind", "--database", "--git", "--expected-revision", "7"}, map[string]any{"database_instance_id": "", "git_instance_id": "", "expected_revision": float64(7)}, `{"web_id":"orders","database_instance_id":"","git_instance_id":"","project_revision":8,"can_manage":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				method := "GET"
				if tc.body != nil {
					method = "PATCH"
				}
				if r.Method != method || r.URL.Path != "/api/v1/web-projects/orders/bindings" {
					t.Errorf("request %s %s", r.Method, r.URL)
				}
				if tc.body != nil {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !reflect.DeepEqual(body, tc.body) {
						t.Errorf("body=%v error=%v", body, err)
					}
				}
				w.Write([]byte(tc.response))
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_bindings")
			args := append([]string{"web", "bindings", tc.args[0], "orders"}, tc.args[1:]...)
			args = append(args, "--json")
			var out, diag bytes.Buffer
			if code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diag); code != 0 || calls.Load() != 1 || !strings.Contains(out.String(), `"status":"succeeded"`) {
				t.Fatal(code, calls.Load(), out.String(), diag.String())
			}
		})
	}
}

func TestWebBindingUnknownWriteReadback(t *testing.T) {
	for _, response := range []string{"lost", `{"web_id":"wrong","project_revision":8}`} {
		t.Run(response, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if response == "lost" {
					conn, _, _ := w.(http.Hijacker).Hijack()
					conn.Close()
					return
				}
				w.Write([]byte(response))
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_bindings")
			var out, diag bytes.Buffer
			code := runCLI(context.Background(), []string{"web", "bindings", "unbind", "orders", "--database", "--expected-revision", "7", "--json"}, strings.NewReader(""), &out, &diag)
			if code != 4 || calls.Load() != 1 || !strings.Contains(out.String(), `"status":"unknown"`) || !strings.Contains(out.String(), "bindings get orders") || !strings.Contains(out.String(), `"database_instance_id":""`) {
				t.Fatal(code, calls.Load(), out.String(), diag.String())
			}
		})
	}
}

func TestWebBindingInput(t *testing.T) {
	for _, args := range [][]string{
		{"bind", "orders", "--database-instance-id", "", "--expected-revision", "7"},
		{"bind", "orders", "--expected-revision", "7"},
		{"bind", "orders", "--git-instance-id", "git-one"},
		{"unbind", "orders", "--expected-revision", "7"},
	} {
		var out, diag bytes.Buffer
		if code := runCLI(context.Background(), append([]string{"web", "bindings"}, args...), strings.NewReader(""), &out, &diag); code != 2 {
			t.Fatalf("%v: %d %s %s", args, code, out.String(), diag.String())
		}
	}
}
