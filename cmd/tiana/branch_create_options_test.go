package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBranchCreateOptions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		flags   []string
		want    string
		invalid bool
	}{
		{"all", []string{"-m", " preview ", "--ttl", "3600", "-ts", "1790000000", "-w"}, `{"name":"preview","notes":" preview ","ttl_seconds":3600,"timestamp":1790000000}`, false},
		{"zero-time", []string{"--timestamp=0", "--message=描述"}, `{"name":"preview","notes":"描述","ttl_seconds":null,"timestamp":0}`, false},
		{"maximum-time", []string{"--timestamp", "18446744073709551615", "--ttl=2592000"}, `{"name":"preview","ttl_seconds":2592000,"timestamp":18446744073709551615}`, false},
		{"negative-ttl", []string{"--ttl=-1"}, "", true},
		{"zero-ttl", []string{"--ttl=0"}, "", true},
		{"overflow-ttl", []string{"--ttl=2592001"}, "", true},
		{"fraction-ttl", []string{"--ttl=1.5"}, "", true},
		{"negative-time", []string{"--timestamp=-1"}, "", true},
		{"overflow-time", []string{"-ts=18446744073709551616"}, "", true},
		{"empty-time", []string{"--timestamp="}, "", true},
		{"long-notes", []string{"-m", strings.Repeat("a", 2049)}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, writes := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				base := "/api/v1/instances/inst_cli"
				switch r.URL.Path {
				case base:
					fmt.Fprint(w, `{"id":"inst_cli","engine":"sqlite"}`)
				case base + "/branches/main":
					fmt.Fprint(w, `{"instance_id":"inst_cli","branch":{"branch_id":"main","name":"main","root":true}}`)
				case base + "/branches/main/children":
					writes++
					var got, want map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Error(err)
					}
					if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
						t.Fatal(err)
					}
					a, _ := json.Marshal(got)
					b, _ := json.Marshal(want)
					if string(a) != string(b) {
						t.Errorf("body=%s want=%s", a, b)
					}
					w.WriteHeader(202)
					fmt.Fprint(w, `{"instance_id":"inst_cli","operation_id":"42"}`)
				case base + "/operations/42":
					fmt.Fprint(w, `{"instance_id":"inst_cli","operation_id":"42","kind":"CREATE_BRANCH","state":"success","branch_id":"child","parent_branch_id":"main"}`)
				default:
					t.Errorf("unexpected %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_cli")
			var out, diag bytes.Buffer
			args := append([]string{"sqlite", "branch", "create", "inst_cli", "preview"}, tc.flags...)
			code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diag)
			if tc.invalid {
				if code != 2 || calls != 0 {
					t.Fatalf("code=%d calls=%d diag=%s", code, calls, &diag)
				}
			} else if code != 0 || writes != 1 {
				t.Fatalf("code=%d writes=%d diag=%s", code, writes, &diag)
			}
		})
	}
}
