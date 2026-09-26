package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestApplicationWorkflowCommandSurface(t *testing.T) {
	var output, diagnostics bytes.Buffer
	code := runCLI(context.Background(), []string{"apps", "--help"}, strings.NewReader(""), &output, &diagnostics)
	if code != 0 {
		t.Fatalf("apps help exit %d: %s", code, diagnostics.String())
	}
	for _, command := range []string{"serve", "create", "upload", "status"} {
		if !strings.Contains(output.String(), command) {
			t.Errorf("apps help missing %s", command)
		}
	}
}

func TestChatLoginCommandSurface(t *testing.T) {
	var output, diagnostics bytes.Buffer
	code := runCLI(context.Background(), []string{"login", "--help"}, strings.NewReader(""), &output, &diagnostics)
	if code != 0 {
		t.Fatalf("login help exit %d: %s", code, diagnostics.String())
	}
	for _, flag := range []string{"--start", "--resume", "--json", "--no-open"} {
		if !strings.Contains(output.String(), flag) {
			t.Errorf("login help missing %s", flag)
		}
	}
}
