package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestThirdPartyServiceCommands(t *testing.T) {
	const base = "/api/v1/third-party-service"
	const markdown = "# Voice\n\n```go\nfmt.Println(\"complete\")\n```\n\nLast sentence."
	const fullKey = "api-0123456789abcdef"
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer access-files" {
			t.Error("third-party management must use the active account")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET " + base + "/catalog":
			filters := r.URL.Query()
			voice := `{"id":100,"slug":"speech.transcriptions","name":"Voice","category_id":1,"provider_id":10}`
			maps := `{"id":101,"slug":"map.search","name":"Maps","category_id":2,"provider_id":11}`
			apis := voice + "," + maps
			switch filters.Encode() {
			case "category=voice&provider=tiana":
				apis = voice
			case "provider=tencent":
				apis = maps
			case "":
			default:
				t.Errorf("unexpected filters %s", filters.Encode())
			}
			_, _ = io.WriteString(w, `{"categories":[{"id":1,"slug":"voice","name":"Voice"},{"id":2,"slug":"maps","name":"Maps"}],"providers":[{"id":10,"slug":"tiana","name":"Tiana"},{"id":11,"slug":"tencent","name":"Tencent"}],"apis":[`+apis+`]}`)
		case "GET " + base + "/apis/speech.transcriptions/docs":
			_ = json.NewEncoder(w).Encode(map[string]any{"slug": "speech.transcriptions", "title": "Voice", "markdown": markdown})
		case "PUT " + base + "/categories/voice":
			var change struct{ Enabled bool }
			if err := json.NewDecoder(r.Body).Decode(&change); err != nil || !change.Enabled {
				t.Errorf("invalid category change %+v %v", change, err)
			}
			w.WriteHeader(http.StatusNoContent)
		case "PUT " + base + "/categories/restricted":
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":{"code":"category_unavailable"}}`)
		case "GET " + base + "/keys":
			_, _ = io.WriteString(w, `[{"id":7,"name":"work","key":"`+fullKey+`","created_at":1,"revoked_at":null}]`)
		case "POST " + base + "/keys":
			var key struct{ Name string }
			if err := json.NewDecoder(r.Body).Decode(&key); err != nil || key.Name != "work" {
				t.Errorf("invalid key creation %+v %v", key, err)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":7,"name":"work","key":"`+fullKey+`","created_at":1,"revoked_at":null}`)
		case "DELETE " + base + "/keys/7":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	env := newTestEnv(t, server.URL)
	fileCredential(t, server.URL, env.credentialsPath)
	command := func(args ...string) string {
		t.Helper()
		var out, diagnostics bytes.Buffer
		if code := runCLI(t.Context(), append([]string{"third-party-service"}, args...), nil, &out, &diagnostics); code != 0 {
			t.Fatalf("%v exited %d: %s", args, code, diagnostics.String())
		}
		return out.String()
	}
	if got := command("api", "docs", "speech.transcriptions"); got != markdown {
		t.Fatalf("raw Markdown mismatch: %q", got)
	}
	var doc struct {
		Status string
		Data   struct{ Slug, Title, Markdown string }
	}
	if got := command("api", "docs", "speech.transcriptions", "--json"); json.Unmarshal([]byte(got), &doc) != nil || doc.Status != "succeeded" || doc.Data.Markdown != markdown || doc.Data.Title != "Voice" || doc.Data.Slug != "speech.transcriptions" {
		t.Fatalf("unexpected JSON document: %s", got)
	}
	if got := command("api", "list", "--category", "voice", "--provider", "tiana", "--json"); !strings.Contains(got, "speech.transcriptions") || strings.Contains(got, "map.search") {
		t.Fatalf("unexpected API list: %s", got)
	}
	if got := command("api", "list", "--provider", "tencent", "--json"); !strings.Contains(got, "map.search") || strings.Contains(got, "speech.transcriptions") {
		t.Fatalf("unexpected provider-only list: %s", got)
	}
	if got := command("api", "show", "speech.transcriptions", "--json"); !strings.Contains(got, `"slug":"speech.transcriptions"`) || !strings.Contains(got, `"provider_slug":"tiana"`) {
		t.Fatalf("unexpected API detail: %s", got)
	}
	if got := command("category", "list", "--page", "2", "--page-size", "1", "--json"); !strings.Contains(got, `"slug":"maps"`) || strings.Contains(got, `"slug":"voice"`) || !strings.Contains(got, `"total_pages":2`) {
		t.Fatalf("unexpected second category page: %s", got)
	}
	if got := command("enable", "--category", "voice"); !strings.Contains(got, "30 seconds") {
		t.Fatalf("missing propagation notice: %s", got)
	}
	if got := command("key", "list", "--json"); strings.Contains(got, fullKey) || !strings.Contains(got, "cdef") || !strings.Contains(got, "active") {
		t.Fatalf("key list output lacks a masked active key")
	}
	if got := command("key", "list"); strings.Contains(got, fullKey) || !strings.Contains(got, "cdef") {
		t.Fatal("terminal list did not mask the Key")
	}
	if got := command("key", "create", "work", "--json"); !strings.Contains(got, fullKey) {
		t.Fatal("successful creation lacks the new credential")
	}
	if got := command("key", "delete", "7"); !strings.Contains(got, "30 seconds") {
		t.Fatalf("missing revocation propagation notice: %s", got)
	}
	var failed, diagnostics bytes.Buffer
	if code := runCLI(t.Context(), []string{"third-party-service", "enable", "--category", "restricted", "--json"}, nil, &failed, &diagnostics); code != 1 || !strings.Contains(failed.String(), "CATEGORY_UNAVAILABLE") {
		t.Fatalf("category rejection=%d %s (%s)", code, failed.String(), diagnostics.String())
	}
	if calls.Load() != 12 {
		t.Fatalf("management requests=%d", calls.Load())
	}
}
