package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestAppServeCLIHelpAndArgumentChecks(t *testing.T) {
	var out, diagnostics bytes.Buffer
	if code := runCLI(context.Background(), []string{"app", "serve", "--help"}, strings.NewReader(""), &out, &diagnostics); code != 0 || !strings.Contains(out.String(), "--dir") {
		t.Fatalf("serve help not registered: %d %s", code, out.String())
	}
	out.Reset()
	diagnostics.Reset()
	if code := runCLI(context.Background(), []string{"app", "serve", "--dir", "/does-not-exist", "--port", "0"}, strings.NewReader(""), &out, &diagnostics); code == 0 {
		t.Fatal("invalid serve arguments accepted")
	}
}
