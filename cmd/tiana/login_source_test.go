package main

import (
	"bytes"
	"encoding/json"
	"github.com/tianacloud/cli/internal/buildconfig"
	"io"
	"net/http"
	"net/http/httptest"
	"os"

	"testing"
)

func TestLoginSourcePriorityAndPendingReuse(t *testing.T) {
	for _, tc := range []struct {
		name, env   string
		buildSource string
		envAbsent   bool
		flags       []string
		want        string
	}{
		{name: "generic default", buildSource: buildconfig.DefaultAcquisitionSource, envAbsent: true, want: "cli"},
		{name: "explicit empty build", envAbsent: true},
		{name: "build default", buildSource: "channel-a", envAbsent: true, want: "channel-a"},
		{name: "environment", env: "channel-b", buildSource: "channel-a", want: "channel-b"},
		{name: "empty environment", buildSource: "channel-a"},
		{name: "flag", buildSource: "channel-a", envAbsent: true, flags: []string{"--from", "channel-c"}, want: "channel-c"},
		{name: "override", env: "invalid environment", buildSource: "channel-a", flags: []string{"--from", "channel-c"}, want: "channel-c"},
		{name: "clear", env: "channel-b", buildSource: "channel-a", flags: []string{"--from", ""}},
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
			if tc.envAbsent {
				if err := os.Unsetenv("TIANA_FROM"); err != nil {
					t.Fatal(err)
				}
			}
			previous := buildconfig.DefaultAcquisitionSource
			buildconfig.DefaultAcquisitionSource = tc.buildSource
			t.Cleanup(func() { buildconfig.DefaultAcquisitionSource = previous })
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

func TestVersionReportsBuildSourceIndependentlyOfEnvironment(t *testing.T) {
	previous := buildconfig.DefaultAcquisitionSource
	buildconfig.DefaultAcquisitionSource = "channel-a"
	t.Cleanup(func() { buildconfig.DefaultAcquisitionSource = previous })
	t.Setenv("TIANA_FROM", "channel-b")
	var out, diag bytes.Buffer
	if code := runCLI(t.Context(), []string{"version", "--json"}, nil, &out, &diag); code != 0 {
		t.Fatalf("code=%d %s", code, &diag)
	}
	var result struct {
		Data struct {
			Source string `json:"default_acquisition_source"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Data.Source != "channel-a" {
		t.Fatalf("source=%q", result.Data.Source)
	}
}
