package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func assertListMessage(t *testing.T, output string) {
	t.Helper()
	header, _, _ := strings.Cut(output, "\n")
	if !strings.HasSuffix(header, "MESSAGE") || strings.Contains(header, "ENGINE") {
		t.Fatalf("unexpected header: %q", header)
	}
	assertSafePresentation(t, output)
	if strings.Contains(output, "\nforged") || !strings.Contains(output, `\nforged`) || !strings.Contains(output, `\t`) {
		t.Fatalf("description controls were not escaped: %q", output)
	}
}

func TestInstanceListMessagesFromAllPages(t *testing.T) {
	for _, product := range []string{"sqlite", "git"} {
		t.Run(product, func(t *testing.T) {
			calls := 0
			webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/api/v1/instances" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				if calls == 1 {
					fmt.Fprintf(w, `{"items":[{"id":"first","display_name":"one","engine":%q,"notes":"中文\nforged\t\u001b[31m\u202e"},{"id":"legacy","display_name":"two","engine":%q}],"total_pages":2}`, product, product)
				} else {
					fmt.Fprintf(w, `{"items":[{"id":"last","display_name":"three","engine":%q,"notes":%q}],"total_pages":2}`, product, strings.Repeat("x", 2048))
				}
			})
			var out, diagnostics bytes.Buffer
			if code := runCLI(t.Context(), []string{product, "list"}, strings.NewReader(""), &out, &diagnostics); code != 0 {
				t.Fatalf("code=%d diagnostics=%s", code, &diagnostics)
			}
			assertListMessage(t, out.String())
			if calls != 2 || !strings.Contains(out.String(), "legacy") || !strings.Contains(out.String(), strings.Repeat("x", 2048)) {
				t.Fatalf("incomplete messages: calls=%d output=%q", calls, out.String())
			}
		})
	}
}

func TestBranchListMessagesFromWire(t *testing.T) {
	calls := 0
	webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" {
			t.Errorf("unexpected mutation: %s", r.Method)
		}
		switch r.URL.Path {
		case "/api/v1/instances/" + testInstanceID:
			io.WriteString(w, sqliteInstanceResponse())
		case "/api/v1/instances/" + testInstanceID + "/branches":
			if r.URL.Query().Get("after") == "child" {
				io.WriteString(w, `{"items":[{"branch_id":"last","name":"later"}]}`)
			} else {
				io.WriteString(w, `{"items":[{"branch_id":"child","name":"preview","notes":"中文\nforged\t\u001b[31m\u202e"},{"branch_id":"main","name":"main","root":true}],"next_cursor":"child"}`)
			}
		default:
			t.Errorf("unexpected path: %s", r.URL)
		}
	})
	var out, diagnostics bytes.Buffer
	if code := runCLI(t.Context(), []string{"sqlite", "branch", "list", testInstanceID}, nil, &out, &diagnostics); code != 0 {
		t.Fatalf("code=%d diagnostics=%s", code, &diagnostics)
	}
	assertListMessage(t, out.String())
	if calls != 3 || !strings.Contains(out.String(), "main") || !strings.Contains(out.String(), "later") || strings.Contains(out.String(), "Next cursor:") {
		t.Fatalf("calls=%d output=%q", calls, out.String())
	}
}
