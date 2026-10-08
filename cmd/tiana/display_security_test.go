package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"unicode"

	"github.com/tianacloud/cli/internal/authclient"
)

func assertSafePresentation(t *testing.T, value string) {
	t.Helper()
	for _, r := range value {
		if (unicode.IsControl(r) && r != '\n') || unicode.Is(unicode.Cf, r) {
			t.Fatalf("unsafe presentation rune %U", r)
		}
	}
	if !strings.Contains(value, "中文") {
		t.Fatal("ordinary Unicode text was lost")
	}
}

func TestManagementPresentationEscapesPeerControlFields(t *testing.T) {
	name := "中文\x1b[31m\r\nforged\u202e"
	instance := authclient.Instance{ID: "test", DisplayName: name, Engine: "git", ProductState: name, StaleReason: name, RuntimeStatusStale: true, CreatedAt: name}
	var table, detail, diagnostics bytes.Buffer
	writeInstanceTable(&table, []authclient.Instance{instance})
	printInstanceDetail(&detail, instance)
	writeCommandError(&diagnostics, errors.New(name))
	for _, value := range []string{table.String(), detail.String(), diagnostics.String()} {
		assertSafePresentation(t, value)
		if strings.Contains(value, "\nforged") {
			t.Fatal("resource field injected an output line")
		}
	}
	if !strings.Contains(table.String(), `\nforged`) {
		t.Fatal("escaped content not visibly preserved")
	}
}

func TestPendingGuidanceEscapesFieldsWithoutMutatingState(t *testing.T) {
	var out bytes.Buffer
	record := authclient.PendingCommand{Command: "git.create", Args: []string{"create", "中文\x1b[31m\nforged"}}
	original := record.Args[1]
	reportUnfinishedOperation(&out, "中文\x1b[31m", record)
	assertSafePresentation(t, out.String())
	if record.Args[1] != original {
		t.Fatal("display mutated pending command")
	}
}
