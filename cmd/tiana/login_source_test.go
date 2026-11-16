package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"

	"testing"
)

func TestLoginSourcePriorityAndPendingReuse(t *testing.T) {
	for _, tc := range []struct {
		name, env string
		flags     []string
		want      string
	}{
		{"unknown", "", nil, ""}, {"environment", "workbuddy", nil, "workbuddy"},
		{"flag", "", []string{"--from", "doubao"}, "doubao"},
		{"override", "invalid environment", []string{"--from", "doubao"}, "doubao"},
		{"clear", "workbuddy", []string{"--from", ""}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			creates := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/auth/transactions":
					creates++
					var body struct {
						Client struct {
							Source string `json:"acquisition_source"`
						} `json:"client"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body.Client.Source != tc.want {
						t.Errorf("source=%q want=%q", body.Client.Source, tc.want)
					}
					io.WriteString(w, `{"transaction_id":"source","client_secret":"secret","user_code":"ABCD-EFGH","verification_uri_complete":"https://example.test/a","expires_in":600}`)
				case "/api/v1/auth/transactions/source/poll":
					io.WriteString(w, `{"status":"pending"}`)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
				}
			}))
			defer server.Close()
			t.Setenv("TIANA_API_ORIGIN", server.URL)
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("TIANA_FROM", tc.env)
			var out, diag bytes.Buffer
			args := append([]string{"login", "--start", "--json", "--no-open"}, tc.flags...)
			if code := runCLI(t.Context(), args, nil, &out, &diag); code != 3 {
				t.Fatalf("code=%d out=%s diag=%s", code, &out, &diag)
			}
			t.Setenv("TIANA_FROM", "changed")
			for _, action := range []string{"--start", "--resume"} {
				out.Reset()
				diag.Reset()
				if code := runCLI(t.Context(), []string{"login", action, "--json", "--no-open", "--from", "new-source"}, nil, &out, &diag); code != 3 {
					t.Fatalf("code=%d %s %s", code, &out, &diag)
				}
			}
			if creates != 1 {
				t.Fatalf("created %d transactions", creates)
			}
		})
	}
}
