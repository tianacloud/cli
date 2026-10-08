package main

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSQLiteBranchListFetchesAllPages(t *testing.T) {
	var cursors []string
	webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/instances/"+testInstanceID {
			io.WriteString(w, sqliteInstanceResponse())
			return
		}
		if r.Method != "GET" || r.URL.Path != "/api/v1/instances/"+testInstanceID+"/branches" || r.URL.Query().Get("search") != "开发" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		after := r.URL.Query().Get("after")
		cursors = append(cursors, after)
		switch after {
		case "start":
			io.WriteString(w, `{"items":[{"branch_id":"first","name":"开发一"}],"next_cursor":"first"}`)
		case "first":
			io.WriteString(w, `{"items":[],"next_cursor":"filtered"}`)
		case "filtered":
			io.WriteString(w, `{"items":[{"branch_id":"last","name":"开发二"}]}`)
		default:
			t.Errorf("unexpected cursor: %q", after)
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	var out, diagnostics bytes.Buffer
	code := runCLI(t.Context(), []string{"sqlite", "branch", "list", testInstanceID, "--after", "start", "--search", "开发"}, nil, &out, &diagnostics)
	if code != 0 || strings.Join(cursors, ",") != "start,first,filtered" || !strings.Contains(out.String(), "开发一") || !strings.Contains(out.String(), "开发二") || strings.Count(out.String(), "BRANCH ID") != 1 || strings.Contains(out.String(), "Next cursor:") {
		t.Fatalf("code=%d cursors=%v stdout=%q stderr=%q", code, cursors, out.String(), diagnostics.String())
	}
}

func TestSQLiteBranchListPageFailureLeavesNoPartialTable(t *testing.T) {
	for _, mode := range []string{"http-error", "cursor-cycle"} {
		t.Run(mode, func(t *testing.T) {
			requests := 0
			webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/instances/"+testInstanceID {
					io.WriteString(w, sqliteInstanceResponse())
					return
				}
				requests++
				if requests == 1 {
					io.WriteString(w, `{"items":[{"branch_id":"first","name":"first"}],"next_cursor":"first"}`)
					return
				}
				if mode == "http-error" {
					w.WriteHeader(http.StatusInternalServerError)
					io.WriteString(w, `{"error":{"code":"INTERNAL"}}`)
				} else {
					io.WriteString(w, `{"items":[{"branch_id":"last","name":"last"}],"next_cursor":"first"}`)
				}
			})
			var out, diagnostics bytes.Buffer
			code := runCLI(t.Context(), []string{"sqlite", "branch", "list", testInstanceID}, nil, &out, &diagnostics)
			if code != 1 || requests != 2 || out.Len() != 0 || diagnostics.Len() == 0 {
				t.Fatalf("code=%d requests=%d stdout=%q stderr=%q", code, requests, out.String(), diagnostics.String())
			}
		})
	}
}
