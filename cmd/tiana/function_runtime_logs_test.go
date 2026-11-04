package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestFunctionRuntimeLogs(t *testing.T) {
	const timestamp = "2026-11-04T12:01:39.123456789Z"
	const message = "hello\npartial\x1b[31m"
	var query url.Values
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/instances/fn-demo":
			t.Error("runtime logs must query the log API directly without runtime status lookup")
			w.Write([]byte(`{"id":"fn-demo","engine":"function","root_runtime_state":"SLEEPING"}`))
		case "/api/v1/functions/fn-demo/runtime-logs":
			query = r.URL.Query()
			if fail {
				w.WriteHeader(503)
				w.Write([]byte(`{"error":{"code":"FUNCTION_LOGS_UNAVAILABLE","message":"unavailable"}}`))
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"items": []map[string]string{{"timestamp": timestamp, "stream": "stderr", "message": message}}, "start": timestamp, "end": timestamp, "next_cursor": "next-page", "retention_hours": 168})
		default:
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_function")
	code, out, diag := runFunction("runtime-logs", "fn-demo", "--start", timestamp, "--end", timestamp, "--stream", "stderr", "--json")
	var result struct {
		Data struct {
			Items      []struct{ Timestamp, Stream, Message string }
			NextCursor string `json:"next_cursor"`
		}
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil || code != 0 || len(result.Data.Items) != 1 {
		t.Fatalf("code=%d out=%s diag=%s", code, out, diag)
	}
	if row := result.Data.Items[0]; row.Timestamp != timestamp || row.Message != message || query.Get("start") != timestamp || query.Get("end") != timestamp || query.Get("stream") != "stderr" || result.Data.NextCursor != "next-page" {
		t.Fatalf("lost record/query: %+v %v", result, query)
	}
	code, out, diag = runFunction("runtime-logs", "fn-demo", "--cursor", "next-page")
	if code != 0 || !strings.Contains(out, "hello\npartial") || strings.Contains(out, "\x1b") || !strings.Contains(out, timestamp) || !strings.Contains(out, "next-page") || query.Encode() != "cursor=next-page" {
		t.Fatalf("code=%d query=%v out=%s diag=%s", code, query, out, diag)
	}
	code, _, diag = runFunction("runtime-logs", "fn-demo", "--since", "1h", "--json")
	start, e1 := time.Parse(time.RFC3339Nano, query.Get("start"))
	end, e2 := time.Parse(time.RFC3339Nano, query.Get("end"))
	if code != 0 || e1 != nil || e2 != nil || end.Sub(start) != time.Hour {
		t.Fatalf("since: %d %v %s", code, query, diag)
	}
	var diagnostics bytes.Buffer
	if code := runCLI(context.Background(), []string{"function", "runtime-logs", "fn-demo"}, nil, failedReceiptWriter{}, &diagnostics); code == 0 {
		t.Fatal("output failure succeeded")
	}
	fail = true
	if code, out, diag := runFunction("runtime-logs", "fn-demo", "--json"); code == 0 || !strings.Contains(out, `"status":"failed"`) || diag == "" {
		t.Fatalf("backend failure: %d %s", code, out)
	}
}
