package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/tianacloud/cli/internal/updatecheck"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionSurface(t *testing.T) {
	for _, args := range [][]string{{"version"}} {
		var out, diagnostics bytes.Buffer
		if code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diagnostics); code != 0 || !strings.Contains(out.String(), "tiana "+version) {
			t.Fatalf("%v: %d %s %s", args, code, &out, &diagnostics)
		}
	}
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"version":"1.0.0"}`)) }))
	defer registry.Close()
	var out, diagnostics bytes.Buffer
	cmd := newVersionCommand(&out, &diagnostics, updatecheck.Checker{Path: filepath.Join(t.TempDir(), "state.json"), Registry: registry.URL, HTTP: registry.Client()})
	if err := cmd.Run(context.Background(), []string{"version", "check", "--skills-version", "dev", "--json"}); err != nil {
		t.Fatalf("check: %v %s %s", err, &out, &diagnostics)
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if _, ok := result["should_notify"]; !ok {
		t.Fatalf("check output: %s", &out)
	}
}
func TestPassiveUpdateScope(t *testing.T) {
	for _, args := range [][]string{{"version", "check", "--json"}, {"git", "remote-helper", "origin", "url"}, {"connect", "--", "native"}, {"web", "list-template", "--json"}, {"__tiana_update_check"}, {"version"}, {"--version"}} {
		if passiveUpdateAllowed(args) {
			t.Fatalf("passive checks for %v", args)
		}
	}
	if !passiveUpdateAllowed([]string{"web", "list"}) {
		t.Fatal("ordinary command was skipped")
	}
}
