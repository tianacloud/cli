package main

import (
	"bytes"
	"context"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testQuotaResponse = `{"tenant_id":"tenant-status","compute_used":"123","storage_used":"456","updated_at":1790083200123,"period_start":1789948800000,"period_end":1790553600000,"instances_used":"2","limits":{"compute":"10000","storage_bytes":"2000000000","max_instances":"3","period":"week"},"blocked":false,"reason":""}`

func TestStatusUsesSavedOriginWithoutEnvironment(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer access-usr_saved" {
			t.Error("unexpected mutation or account")
		}
		switch r.URL.Path {
		case "/api/v1/auth/transactions/whoami":
			io.WriteString(w, `{"user":{"user_id":"usr_saved","email":"saved@example.test"}}`)
		case "/api/v1/usage":
			io.WriteString(w, testQuotaResponse)
		default:
			t.Error("unexpected request")
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_saved")
	t.Setenv("TIANA_API_ORIGIN", "")
	if err := os.Unsetenv("TIANA_API_ORIGIN"); err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	if code := runCLI(context.Background(), []string{"--ca-file", caFile, "status"}, nil, &out, &diag); code != 0 || requests != 2 || !strings.Contains(out.String(), "saved@example.test") {
		t.Fatalf("code=%d requests=%d out=%s diagnostics=%s", code, requests, &out, &diag)
	}
}

func TestStatusLoginAndQuota(t *testing.T) {
	for _, mode := range []string{"success", "unknown", "maximum", "blocked", "quota-failed", "quota-malformed", "quota-bad-number", "quota-null-required", "signed-out"} {
		t.Run(mode, func(t *testing.T) {
			authCalls, usageCalls := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("unexpected mutation/login")
					w.WriteHeader(500)
					return
				}
				switch r.URL.Path {
				case "/api/v1/auth/transactions/whoami":
					authCalls++
					io.WriteString(w, `{"user":{"user_id":"usr_status","email":"status@example.test","display_name":"Test User","username":"testuser"},"access_token":"PRIVATE_ACCOUNT_SECRET"}`)
				case "/api/v1/usage":
					usageCalls++
					if r.URL.Query().Get("page_size") != "1" || r.URL.Query().Get("page") != "1" || len(r.URL.Query()) != 2 {
						t.Error("unexpected usage selection")
					}
					if mode == "quota-failed" {
						w.WriteHeader(404)
						io.WriteString(w, `{"error":{"code":"NOT_FOUND","message":"PRIVATE_QUOTA_SECRET"}}`)
						return
					}
					if mode == "quota-malformed" {
						io.WriteString(w, `{"tenant_id":"tenant-status"}`)
						return
					}
					body := testQuotaResponse
					if mode == "quota-bad-number" {
						body = strings.ReplaceAll(body, `"compute_used":"123"`, `"compute_used":"PRIVATE_BAD_NUMBER"`)
					}
					if mode == "quota-null-required" {
						body = strings.ReplaceAll(body, `"compute":"10000"`, `"compute":null`)
					}
					if mode == "unknown" {
						body = strings.ReplaceAll(body, `"storage_used":"456"`, `"storage_used":null`)
						body = strings.ReplaceAll(body, `"updated_at":1790083200123`, `"updated_at":null`)
					}
					if mode == "maximum" {
						body = strings.ReplaceAll(body, `"compute_used":"123"`, `"compute_used":"18446744073709551615"`)
						body = strings.ReplaceAll(body, `"compute":"10000"`, `"compute":"0"`)
					}
					if mode == "blocked" {
						body = strings.ReplaceAll(body, `"blocked":false,"reason":""`, `"blocked":true,"reason":"COMPUTE"`)
					}
					io.WriteString(w, body)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			if mode != "signed-out" {
				saveTestCredential(t, server.URL, env.credentialsPath, "usr_status")
			}
			if err := os.WriteFile(env.tokensPath, []byte("PRIVATE_LOCAL_TOKEN"), 0600); err != nil {
				t.Fatal(err)
			}
			var out, diag bytes.Buffer
			code := runCLI(context.Background(), []string{"status"}, strings.NewReader(""), &out, &diag)
			if strings.Contains(out.String()+diag.String(), "PRIVATE_") {
				t.Fatal("status leaked credentials/error body")
			}
			for _, hidden := range []string{"Signed in as", "User ID:", "Management:", "Tenant:", "usr_status", "tenant-status", server.URL} {
				if strings.Contains(out.String(), hidden) {
					t.Errorf("status displays removed field %q: %s", hidden, &out)
				}
			}
			if mode == "signed-out" {
				if code != 1 || authCalls != 0 || usageCalls != 0 || !strings.Contains(out.String(), "Not signed in") || !strings.Contains(diag.String(), "tiana login") {
					t.Fatalf("code=%d requests=%d/%d out=%s diag=%s", code, authCalls, usageCalls, &out, &diag)
				}
				return
			}
			if !strings.Contains(out.String(), "status@example.test") {
				t.Fatalf("missing account: %s", &out)
			}
			if strings.HasPrefix(mode, "quota-") {
				if code != 1 || !strings.Contains(out.String(), "Quota: unavailable") {
					t.Fatalf("code=%d out=%s diag=%s", code, &out, &diag)
				}
				return
			}
			if code != 0 || authCalls != 1 || usageCalls != 1 || diag.Len() != 0 {
				t.Fatalf("code=%d requests=%d/%d diag=%s", code, authCalls, usageCalls, &diag)
			}
			for _, want := range []string{"Compute", "Storage (bytes)", "Instances", "week", "2026-09-21T00:00:00Z", "2000000000"} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("missing %s: %s", want, &out)
				}
			}
			if mode == "success" {
				for _, want := range []string{"USAGE", "PROGRESS", "1.2%", "<0.1%", "66.7%", "[█████████████░░░░░░░]"} {
					if !strings.Contains(out.String(), want) {
						t.Errorf("missing quota progress %q: %s", want, &out)
					}
				}
			}
			if mode == "unknown" && strings.Count(out.String(), "unknown") < 2 {
				t.Fatalf("unknown shown as zero: %s", &out)
			}
			if mode == "maximum" && !strings.Contains(out.String(), "18446744073709551615") {
				t.Fatalf("lost uint64 precision: %s", &out)
			}
			if mode == "blocked" && (!strings.Contains(out.String(), "Blocked: yes") || !strings.Contains(out.String(), "COMPUTE")) {
				t.Fatalf("blocked state absent: %s", &out)
			}
		})
	}
}

func TestStatusCommandReplacesWhoami(t *testing.T) {
	t.Setenv("TIANA_API_ORIGIN", "")
	for _, args := range [][]string{{"whoami"}, {"whoami", "--help"}, {"help", "whoami"}, {"status", "extra"}} {
		var out, diag bytes.Buffer
		if code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diag); code != 2 || out.Len() != 0 {
			t.Fatalf("args=%v code=%d out=%s", args, code, &out)
		}
	}
	var out, diag bytes.Buffer
	if code := runCLI(context.Background(), []string{"--help"}, strings.NewReader(""), &out, &diag); code != 0 || strings.Contains(out.String(), "whoami") || !strings.Contains(out.String(), "status") {
		t.Fatalf("wrong help: %s", &out)
	}
}

func TestStatusQuotaProgress(t *testing.T) {
	zero, one, two, max := uint64(0), uint64(1), uint64(2), ^uint64(0)
	belowMax := max - 1
	for _, tc := range []struct {
		name         string
		used         *uint64
		limit        uint64
		percent, bar string
	}{
		{"empty", &zero, 10, "0.0%", "[░░░░░░░░░░░░░░░░░░░░]"},
		{"half", &one, 2, "50.0%", "[██████████░░░░░░░░░░]"},
		{"rounded", &two, 3, "66.7%", "[█████████████░░░░░░░]"},
		{"full", &two, 2, "100.0%", "[████████████████████]"},
		{"over", &two, 1, "200.0%", "[████████████████████]"},
		{"tiny", &one, max, "<0.1%", "[░░░░░░░░░░░░░░░░░░░░]"},
		{"near-full", &belowMax, max, ">99.9%", "[███████████████████░]"},
		{"just-over", &max, belowMax, ">100.0%", "[████████████████████]"},
		{"maximum", &max, 1, "1844674407370955161500.0%", "[████████████████████]"},
		{"max-equal", &max, max, "100.0%", "[████████████████████]"},
		{"unknown", nil, 10, "unknown", "-"},
		{"unknown-zero", nil, 0, "unknown", "-"},
		{"zero-limit-empty", &zero, 0, "n/a", "-"},
		{"zero-limit-used", &max, 0, "n/a", "-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			percent, bar := statusQuotaProgress(tc.used, tc.limit)
			if percent != tc.percent || bar != tc.bar {
				t.Fatalf("got %s %s; want %s %s", percent, bar, tc.percent, tc.bar)
			}
		})
	}
}
