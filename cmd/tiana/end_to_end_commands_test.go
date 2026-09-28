package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestApplicationWorkflowCommandSurface(t *testing.T) {
	var output, diagnostics bytes.Buffer
	code := runCLI(context.Background(), []string{"app", "--help"}, strings.NewReader(""), &output, &diagnostics)
	if code != 0 {
		t.Fatalf("app help exit %d: %s", code, diagnostics.String())
	}
	for _, command := range []string{"serve", "create", "upload", "status"} {
		if !strings.Contains(output.String(), command) {
			t.Errorf("app help missing %s", command)
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

func TestAppCommandReplacesWeb(t *testing.T) {
	for _, args := range [][]string{{"web"}, {"web", "--help"}, {"help", "web"}, {"web", "create", "--project", "billing"}} {
		var output, diagnostics bytes.Buffer
		if code := runCLI(context.Background(), args, nil, &output, &diagnostics); code != 2 || output.Len() != 0 {
			t.Fatalf("removed web accepted: args=%v code=%d out=%s", args, code, &output)
		}
	}
	var output, diagnostics bytes.Buffer
	if code := runCLI(context.Background(), []string{"--help"}, nil, &output, &diagnostics); code != 0 {
		t.Fatalf("root help failed: %d %s", code, &diagnostics)
	}
	_, products, found := strings.Cut(output.String(), "Products:")
	if !found || strings.Contains(output.String(), "Resources:") {
		t.Fatalf("wrong group: %s", &output)
	}
	products, _, _ = strings.Cut(products, "GLOBAL OPTIONS:")
	var names []string
	for _, line := range strings.Split(products, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			names = append(names, strings.TrimSuffix(fields[0], ","))
		}
	}
	if strings.Join(names, ",") != "sqlite,git,app" {
		t.Fatalf("wrong products: %v", names)
	}
}
