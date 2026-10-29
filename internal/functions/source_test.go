package functions

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/tianacloud/sdk-go/fetch"
)

type testSourceTransport struct {
	do func(fetch.Request) (*fetch.Response, error)
}

func (t testSourceTransport) Close() {}
func (t testSourceTransport) Do(_ context.Context, request fetch.Request) (*fetch.Response, error) {
	return t.do(request)
}
func sourceResponse(status int, body string) (*fetch.Response, error) {
	return &fetch.Response{Status: status, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestSharedSourceSelectionAndSave(t *testing.T) {
	for _, tc := range []struct {
		name, folder       string
		rootGo             bool
		wantRoot           string
		initial, wantError bool
	}{
		{"function directory", "directory", true, "functions/fn-demo", false, false},
		{"new function in Go repository", "missing", true, "functions/fn-demo", true, false},
		{"frontend repository", "missing", false, "functions/fn-demo", true, false},
		{"new owned repository", "empty", false, "functions/fn-demo", true, false},
		{"invalid directory", "file", true, "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var saved map[string]any
			s := &Source{functionID: "fn-demo", transport: testSourceTransport{do: func(request fetch.Request) (*fetch.Response, error) {
				u, _ := url.Parse(request.PathQuery)
				if request.Method == "POST" {
					if err := json.NewDecoder(request.Body).Decode(&saved); err != nil {
						t.Fatal(err)
					}
					return sourceResponse(200, `{"commit_oid":"new-commit"}`)
				}
				if u.Path == "/v1/file" {
					if u.Query().Get("path") != tc.wantRoot+"/handler.go" {
						t.Errorf("wrong file path: %s", u)
					}
					return sourceResponse(200, "package main")
				}
				if u.Query().Get("path") == "functions/fn-demo" {
					if u.Query().Get("commit") != "old-commit" {
						t.Errorf("directory read was not pinned: %s", u)
					}
					switch tc.folder {
					case "directory":
						return sourceResponse(200, `{"resolved_commit_oid":"old-commit","entries":[{"name":"handler.go","type":"blob"}],"next_offset":null}`)
					case "file":
						return sourceResponse(409, `{"error":{"code":"NOT_A_DIRECTORY"}}`)
					default:
						return sourceResponse(404, `{"error":{"code":"NOT_FOUND"}}`)
					}
				}
				if tc.folder == "empty" {
					return sourceResponse(200, `{"empty":true,"entries":[],"next_offset":null}`)
				}
				if tc.rootGo {
					return sourceResponse(200, `{"resolved_commit_oid":"old-commit","entries":[{"name":"main.go","type":"blob"},{"name":"frontend","type":"tree"}],"next_offset":null}`)
				}
				return sourceResponse(200, `{"resolved_commit_oid":"old-commit","entries":[{"name":"package.json","type":"blob"}],"next_offset":null}`)
			}}}
			view, err := s.Select(t.Context(), "")
			if tc.wantError {
				if err == nil {
					t.Fatal("expected function directory error")
				}
				return
			}
			if err != nil || view.Root != tc.wantRoot || view.Sample != tc.initial {
				t.Fatalf("view=%+v error=%v", view, err)
			}
			commit, err := s.Save(t.Context(), view, map[string]string{"handler.go": "package main"})
			if err != nil || commit != "new-commit" {
				t.Fatalf("save=%s %v", commit, err)
			}
			changes := saved["files"].(map[string]any)
			wantPath := "handler.go"
			if tc.wantRoot != "" {
				wantPath = tc.wantRoot + "/handler.go"
			}
			if len(changes) != 1 || changes[wantPath] != "package main" {
				t.Fatalf("unexpected files: %v", changes)
			}
			if view.Commit == "" {
				if saved["base_commit"] != nil {
					t.Fatal("initial save needs null base")
				}
			} else if saved["base_commit"] != view.Commit {
				t.Fatal("base commit lost")
			}
		})
	}
}

func TestSourceSaveReportsConcurrentCommit(t *testing.T) {
	s := &Source{transport: testSourceTransport{do: func(fetch.Request) (*fetch.Response, error) {
		return sourceResponse(409, `{"error":{"code":"SOURCE_CHANGED"}}`)
	}}}
	_, err := s.Save(t.Context(), SourceView{Commit: "old", Root: "functions/fn-demo"}, map[string]string{"main.go": "package main"})
	if err == nil || !strings.Contains(err.Error(), "reconcile") {
		t.Fatalf("error=%v", err)
	}
}

func TestBuildTerminalStates(t *testing.T) {
	for _, state := range []string{"queued", "compiling", "uploading", "published", "failed", "cancelled", "superseded", "deleted"} {
		done, err := (Submission{ID: "1", Status: state}).Terminal()
		wantDone := state == "published" || state == "failed" || state == "cancelled" || state == "superseded" || state == "deleted"
		if done != wantDone || (err != nil) != (wantDone && state != "published") {
			t.Fatalf("state=%s done=%v err=%v", state, done, err)
		}
	}
}
