package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/tianacloud/cli/internal/authclient"
)

func runGitManagement(args []string) (int, string, string) {
	var out, diag bytes.Buffer
	code := runCLI(context.Background(), append([]string{"git"}, args...), strings.NewReader(""), &out, &diag)
	return code, out.String(), diag.String()
}

func gitInstanceResponse() string {
	return strings.ReplaceAll(instanceResponse(testInstanceID, "my-repo", testEndpointID), `"engine":"sqlite"`, `"engine":"git"`)
}

func TestGitManagementHelpAndArguments(t *testing.T) {
	for _, command := range []string{"create", "list", "show"} {
		code, out, diag := runGitManagement([]string{command, "--help"})
		if code != 0 || !strings.Contains(out, "tiana git "+command) {
			t.Fatalf("%s: code=%d out=%s diag=%s", command, code, out, diag)
		}
	}
	for _, args := range [][]string{{"create"}, {"create", "a", "b"}, {"create", "a", "--display-name", "b"}, {"create", "a", "--engine", "sqlite"}, {"list", "extra"}, {"show"}, {"show", "a", "b"}, {"show", "a", "--branch", "main"}} {
		if code, out, diag := runGitManagement(args); code != 2 || out != "" {
			t.Fatalf("%v: code=%d out=%s diag=%s", args, code, out, diag)
		}
	}
}

func TestGitShowEngineAndURL(t *testing.T) {
	host := testEndpointID + ".db.env.tiana.test"
	for _, tc := range []struct {
		name, engine, url, endpoint, hostname string
		wantCode                              int
		want                                  string
	}{
		{"git", "git", "https://" + host, testEndpointID, host, 0, "tiana://" + host + "/repo.git\n"},
		{"port", "git", "https://" + host + ":18445", testEndpointID, host, 0, "tiana://" + host + ":18445/repo.git\n"},
		{"sqlite", "sqlite", "https://" + host, testEndpointID, host, 1, ""},
		{"unknown engine", "", "https://" + host, testEndpointID, host, 1, ""},
		{"unpublished legacy engine", "app_git", "https://" + host, testEndpointID, host, 1, ""},
		{"missing url", "git", "", testEndpointID, host, 1, ""},
		{"userinfo", "git", "https://secret@" + host, testEndpointID, host, 1, ""},
		{"path", "git", "https://" + host + "/wrong", testEndpointID, host, 1, ""},
		{"endpoint mismatch", "git", "https://" + host, "ep-00000000000000000000000000", host, 1, ""},
		{"hostname mismatch", "git", "https://" + host, testEndpointID, testEndpointID + ".other.test", 1, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/instances/"+testInstanceID {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
					return
				}
				json.NewEncoder(w).Encode(authclient.Instance{ID: testInstanceID, Engine: tc.engine, EndpointID: tc.endpoint, Connection: &authclient.InstanceConnection{URL: tc.url, Hostname: tc.hostname}})
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_git")
			code, out, diag := runGitManagement([]string{"show", testInstanceID, "--url"})
			if code != tc.wantCode || out != tc.want || calls != 1 || strings.Contains(diag, "secret") {
				t.Fatalf("code=%d out=%q calls=%d diag=%s", code, out, calls, diag)
			}
		})
	}
}

func TestGitListPagination(t *testing.T) {
	for _, failLast := range []bool{false, true} {
		t.Run(fmt.Sprint(failLast), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/api/v1/instances" || len(r.URL.Query()) != 2 || r.URL.Query().Get("page") != fmt.Sprint(calls) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				if calls == 3 && failLast {
					http.Error(w, `{"error":{"code":"UNAVAILABLE"}}`, 503)
					return
				}
				item := gitInstanceResponse()
				if calls == 2 {
					item = sqliteInstanceResponse()
				}
				fmt.Fprintf(w, `{"items":[%s],"page":%d,"page_size":20,"total":41,"total_pages":3}`, item, calls)
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_git")
			code, out, diag := runGitManagement([]string{"list"})
			if failLast {
				if code != 1 || out != "" {
					t.Fatalf("partial list: code=%d bytes=%d", code, len(out))
				}
			} else if code != 0 || strings.Count(out, "my-repo") != 2 || strings.Contains(out, "sqlite") || !strings.Contains(out, "tiana://") {
				t.Fatalf("code=%d out=%s diag=%s", code, out, diag)
			}
			if calls != 3 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}

func TestGitCreateRefusesSQLitePending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s", r.URL)
		http.Error(w, "unexpected", 500)
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_git")
	store := authclient.NewFilePendingCommandStore(env.pendingPath)
	pending := authclient.PendingCommand{Command: "db.create", Args: []string{"create", "my-repo"}, Origin: server.URL, UserID: "usr_git", IdempotencyKey: "sqlite-key"}
	if err := store.Save(pending); err != nil {
		t.Fatal(err)
	}
	if code, _, diag := runGitManagement(pending.Args); code != 1 || !strings.Contains(diag, "tiana sqlite 'create'") {
		t.Fatalf("code=%d diag=%s", code, diag)
	}
	after, err := store.Load()
	if err != nil || !reflect.DeepEqual(pending, after) {
		t.Fatal("changed SQLite pending")
	}
}

func TestGitShowNameAndDetail(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprint(duplicate), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch r.URL.Path {
				case "/api/v1/instances/my-repo":
					w.WriteHeader(400)
					io.WriteString(w, `{"error":{"code":"INVALID_INSTANCE_ID"}}`)
				case "/api/v1/instances":
					if len(r.URL.Query()) != 3 || r.URL.Query().Get("display_name") != "my-repo" || r.URL.Query().Get("page_size") != "20" {
						t.Error("name lookup must request exact display_name with bounded pagination")
						http.Error(w, `{"error":{"code":"INVALID_ARGUMENT"}}`, 400)
						return
					}
					page := r.URL.Query().Get("page")
					item := gitInstanceResponse()
					if page == "1" {
						item = strings.ReplaceAll(sqliteInstanceResponse(), "sqlite-db", "my-repo") + "," + strings.ReplaceAll(gitInstanceResponse(), "my-repo", "my-repo-extra")
					}
					if page == "3" && !duplicate {
						item = strings.ReplaceAll(gitInstanceResponse(), "my-repo", "other")
					}
					fmt.Fprintf(w, `{"items":[%s],"total":41,"total_pages":3}`, item)
				default:
					t.Errorf("unexpected request: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_git")
			code, out, diag := runGitManagement([]string{"show", "my-repo"})
			if calls != 4 {
				t.Fatalf("calls=%d", calls)
			}
			if duplicate {
				if code != 1 || out != "" || !strings.Contains(diag, "specify an instance ID") {
					t.Fatalf("code=%d diag=%s", code, diag)
				}
			} else if code != 0 || !strings.Contains(out, "my-repo") || !strings.Contains(out, "git") || !strings.Contains(out, "tiana://") {
				t.Fatalf("code=%d out=%s diag=%s", code, out, diag)
			}
		})
	}
}

func TestGitShowNameQueryEncodingAndNormalization(t *testing.T) {
	name := "开发 + %_ /&= Repo"
	for _, reference := range []string{name, "  " + name + "  "} {
		t.Run(reference, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet {
					t.Errorf("unexpected method: %s", r.Method)
				}
				if r.URL.Path != "/api/v1/instances" {
					w.WriteHeader(http.StatusBadRequest)
					io.WriteString(w, `{"error":{"code":"INVALID_INSTANCE_ID"}}`)
					return
				}
				if r.URL.Query().Get("display_name") != name || r.URL.Query().Get("page") != "1" || r.URL.Query().Get("page_size") != "20" || len(r.URL.Query()) != 3 {
					t.Errorf("incorrect name query: %s", r.URL)
				}
				instance := authclient.Instance{ID: testInstanceID, DisplayName: name, Engine: "git"}
				_ = json.NewEncoder(w).Encode(authclient.InstancePage{Items: []authclient.Instance{instance}, Total: 1, TotalPages: 1, Page: 1, PageSize: 20})
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_git")
			code, out, diag := runGitManagement([]string{"show", "--", reference})
			if code != 0 || !strings.Contains(out, name) || calls != 2 {
				t.Fatalf("code=%d calls=%d out=%s diag=%s", code, calls, out, diag)
			}
		})
	}
}

func TestGitCreateUnavailableEngine(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" || r.URL.Path != "/api/v1/app-types" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		io.WriteString(w, appTypesResponse("sqlite"))
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_git")
	code, out, diag := runGitManagement([]string{"create", "my-repo"})
	if code != 2 || out != "" || requests != 1 || !strings.Contains(diag, `engine "git" is not available`) {
		t.Fatalf("code=%d requests=%d diag=%s", code, requests, diag)
	}
	if _, err := authclient.NewFilePendingCommandStore(env.pendingPath).Load(); !errors.Is(err, authclient.ErrPendingNotFound) {
		t.Fatal("unavailable engine left intent")
	}
}

func TestGitCreateResumeRejectsChangedMetadata(t *testing.T) {
	for _, change := range []string{"engine", "identity"} {
		t.Run(change, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/v1/instances/"+testInstanceID {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
					return
				}
				body := gitInstanceResponse()
				if change == "engine" {
					body = sqliteInstanceResponse()
				} else {
					body = strings.ReplaceAll(body, testInstanceID, "inst_other")
				}
				io.WriteString(w, body)
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_git")
			store := authclient.NewFilePendingCommandStore(env.pendingPath)
			pending := authclient.PendingCommand{Command: "git.create", Args: []string{"create", "my-repo"}, Origin: server.URL, UserID: "usr_git", InstanceID: testInstanceID, IdempotencyKey: "instance-key"}
			if err := store.Save(pending); err != nil {
				t.Fatal(err)
			}
			code, out, diag := runGitManagement(pending.Args)
			if code != 1 || out != "" {
				t.Fatalf("code=%d diag=%s", code, diag)
			}
			after, err := store.Load()
			if err != nil || !reflect.DeepEqual(pending, after) {
				t.Fatal("changed metadata discarded recovery intent")
			}
		})
	}
}

func TestGitAllPagesAndEmptyList(t *testing.T) {
	pages := 0
	fetch := func(_ context.Context, page, size int) (authclient.InstancePage, error) {
		pages++
		engine := "sqlite"
		if page == 2 {
			engine = "git"
		}
		return authclient.InstancePage{Items: []authclient.Instance{{ID: fmt.Sprint(page), DisplayName: fmt.Sprintf("row-%d", page), Engine: engine}}, Page: page, PageSize: size, Total: 2, TotalPages: 2}, nil
	}
	var out bytes.Buffer
	items, err := fetchAllInstancesScoped(context.Background(), fetch, nonInteractivePageSize, gitManagementScope)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeInstanceTable(&out, items); err != nil {
		t.Fatal(err)
	}
	if pages != 2 || !strings.Contains(out.String(), "row-2") || strings.Contains(out.String(), "row-1") || strings.Contains(out.String(), "ENGINE") {
		t.Fatalf("pages=%d out=%s", pages, &out)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"items":[],"total":0,"total_pages":0}`)
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_git")
	if code, result, diag := runGitManagement([]string{"list"}); code != 0 || result != "No Git repositories found.\n" {
		t.Fatalf("code=%d out=%s diag=%s", code, result, diag)
	}
}
