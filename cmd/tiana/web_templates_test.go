package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestWebTemplateCommandSurface(t *testing.T) {
	for _, command := range []string{"list-template", "init-template"} {
		var out, diagnostics bytes.Buffer
		if code := runCLI(context.Background(), []string{"web", command, "--help"}, strings.NewReader(""), &out, &diagnostics); code != 0 || !strings.Contains(out.String(), command) {
			t.Fatalf("%s help: %d %s %s", command, code, &out, &diagnostics)
		}
	}
	for _, args := range [][]string{{"web", "init-template", "notes"}, {"web", "init-template", "--dir", "target"}, {"web", "list-template", "notes"}} {
		var out, diagnostics bytes.Buffer
		if code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diagnostics); code != 2 {
			t.Fatalf("invalid args %v: %d %s %s", args, code, &out, &diagnostics)
		}
	}
}
