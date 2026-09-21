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

func TestGitCreateResumesAndSeparatesSQLiteIntent(t *testing.T) {
	for _, failure := range []string{"instance", "token"} {
		t.Run(failure, func(t *testing.T) {
			creates, tokens := 0, 0
			var keys, bodies []string
			var pendingPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/v1/app-types":
					io.WriteString(w, appTypesResponse("sqlite", "git"))
				case r.URL.Path == "/api/v1/instances" && r.Method == "POST":
					creates++
					var request authclient.CreateInstanceRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Engine != "git" || request.DisplayName != "my-repo" {
						t.Errorf("unexpected create: %+v %v", request, err)
					}
					if failure == "instance" {
						keys = append(keys, r.Header.Get("Idempotency-Key"))
					}
					pending, err := authclient.NewFilePendingCommandStore(pendingPath).Load()
					if err != nil || pending.Command != "git.create" {
						t.Error("Git intent not saved before POST")
					}
					if failure == "instance" && creates == 1 {
						http.Error(w, `{"error":{"code":"INTERNAL"}}`, 500)
						return
					}
					w.WriteHeader(201)
					io.WriteString(w, gitInstanceResponse())
				case r.URL.Path == "/api/v1/instances/"+testInstanceID && r.Method == "GET":
					io.WriteString(w, gitInstanceResponse())
				case r.URL.Path == "/api/v1/instances/"+testInstanceID+"/endpoints/"+testEndpointID+"/tokens" && r.Method == "POST":
					tokens++
					pending, err := authclient.NewFilePendingCommandStore(pendingPath).Load()
					if err != nil || pending.EndpointID != testEndpointID || pending.TokenIdempotencyKey != r.Header.Get("Idempotency-Key") {
						t.Error("Token target not persisted before POST")
					}
					if failure == "token" {
						keys = append(keys, r.Header.Get("Idempotency-Key"))
						b, _ := io.ReadAll(r.Body)
						bodies = append(bodies, string(b))
					}
					if failure == "token" && tokens == 1 {
						http.Error(w, `{"error":{"code":"INTERNAL"}}`, 500)
						return
					}
					w.WriteHeader(201)
					io.WriteString(w, tokenCreateResponse())
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			pendingPath = env.pendingPath
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_git")
			args := []string{"create", "my-repo"}
			if code, _, diag := runGitManagement(args); code != 1 {
				t.Fatalf("first code=%d diag=%s", code, diag)
			}
			store := authclient.NewFilePendingCommandStore(env.pendingPath)
			pending, err := store.Load()
			if err != nil || pending.Command != "git.create" || !reflect.DeepEqual(pending.Args, args) {
				t.Fatalf("invalid Git intent: %v", err)
			}
			var out, diag bytes.Buffer
			before := creates + tokens
			if code := runSQLite(context.Background(), args, strings.NewReader(""), &out, &diag); code != 1 || creates+tokens != before || !strings.Contains(diag.String(), "tiana git 'create'") {
				t.Fatalf("SQLite resumed Git intent: %d %s", code, &diag)
			}
			after, err := store.Load()
			if err != nil || !reflect.DeepEqual(pending, after) {
				t.Fatal("SQLite changed Git intent")
			}
			code, result, diagnostics := runGitManagement(args)
			if code != 0 || !strings.Contains(result, "tiana://"+testEndpointID+".db.example.test/repo.git") || !strings.Contains(result, "Token: "+testToken) {
				t.Fatalf("resume code=%d diag=%s", code, diagnostics)
			}
			if len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] || (failure == "token" && (creates != 1 || bodies[0] != bodies[1])) || (failure == "instance" && tokens != 1) {
				t.Fatal("retry changed identity or duplicated creation")
			}
			if _, err := store.Load(); !errors.Is(err, authclient.ErrPendingNotFound) {
				t.Fatal("pending not removed")
			}
			if len(loadStoredTokens(t, env.tokensPath)) != 1 {
				t.Fatal("token not saved")
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
	for _, change := range []string{"engine", "endpoint"} {
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
					body = strings.ReplaceAll(body, testEndpointID, "ep-00000000000000000000000000")
				}
				io.WriteString(w, body)
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_git")
			store := authclient.NewFilePendingCommandStore(env.pendingPath)
			pending := authclient.PendingCommand{Command: "git.create", Args: []string{"create", "my-repo"}, Origin: server.URL, UserID: "usr_git", InstanceID: testInstanceID, EndpointID: testEndpointID, IdempotencyKey: "instance-key", TokenIdempotencyKey: "token-key", TokenRequestID: "request-id", ExpiresAt: -1, Step: "token"}
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

func TestGitInteractiveAndEmptyList(t *testing.T) {
	pages := 0
	fetch := func(_ context.Context, page, size int) (authclient.InstancePage, error) {
		pages++
		engine := "sqlite"
		if page == 2 {
			engine = "git"
		}
		return authclient.InstancePage{Items: []authclient.Instance{{ID: fmt.Sprint(page), Engine: engine}}, Page: page, PageSize: size, Total: 2, TotalPages: 2}, nil
	}
	var out bytes.Buffer
	if err := runInteractiveListScoped(context.Background(), fetch, strings.NewReader("\n"), &out, gitManagementScope); err != nil {
		t.Fatal(err)
	}
	if pages != 2 || !strings.Contains(out.String(), "No Git repositories on this page.") || !strings.Contains(out.String(), "git") || strings.Contains(out.String(), "SQLite") {
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

func TestGitCreateAcceptedOperationRecovery(t *testing.T) {
	for _, mode := range []string{"success", "interrupted", "not-visible", "failed", "wrong-operation", "running"} {
		t.Run(mode, func(t *testing.T) {
			creates, tokens, polls, gets := 0, 0, 0, 0
			completed := false
			var pendingPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/v1/app-types":
					io.WriteString(w, appTypesResponse("git"))
				case r.URL.Path == "/api/v1/instances" && r.Method == "POST":
					creates++
					w.WriteHeader(202)
					fmt.Fprintf(w, `{"instance_id":%q,"operation_id":"17"}`, testInstanceID)
				case r.URL.Path == "/api/v1/instances/"+testInstanceID && r.Method == "GET":
					gets++
					if mode == "not-visible" && gets <= 2 {
						w.WriteHeader(404)
						io.WriteString(w, `{"error":{"code":"INSTANCE_NOT_FOUND"}}`)
						return
					}
					if completed {
						io.WriteString(w, strings.TrimSuffix(gitInstanceResponse(), "}")+`,"creation_operation_id":"17"}`)
					} else {
						fmt.Fprintf(w, `{"id":%q,"display_name":"my-repo","engine":"git","creation_operation_id":"17","product_state":"CREATING"}`, testInstanceID)
					}
				case r.URL.Path == "/api/v1/instances/"+testInstanceID+"/operations/17" && r.Method == "GET":
					polls++
					pending, err := authclient.NewFilePendingCommandStore(pendingPath).Load()
					if err != nil || pending.InstanceID != testInstanceID || pending.Step != "instance" || pending.OperationID != "" {
						t.Error("accepted instance not persisted separately from Token recovery")
					}
					if mode == "interrupted" && polls == 1 {
						http.Error(w, `{"error":{"code":"UNAVAILABLE"}}`, 503)
						return
					}
					state, op := "success", "17"
					if mode == "running" && polls == 1 {
						state = "running"
					}
					if mode == "failed" {
						state = "failed"
					}
					if mode == "wrong-operation" {
						op = "18"
					}
					completed = state == "success" && op == "17"
					fmt.Fprintf(w, `{"instance_id":%q,"operation_id":%q,"kind":"CREATE_INSTANCE","state":%q}`, testInstanceID, op, state)
				case r.URL.Path == "/api/v1/instances/"+testInstanceID+"/endpoints/"+testEndpointID+"/tokens" && r.Method == "POST":
					if !completed {
						t.Error("Token before successful creation")
					}
					tokens++
					w.WriteHeader(201)
					io.WriteString(w, tokenCreateResponse())
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			pendingPath = env.pendingPath
			saveTestCredential(t, server.URL, env.credentialsPath, "usr_git")
			args := []string{"create", "my-repo"}
			code, out, diag := runGitManagement(args)
			if mode == "interrupted" || mode == "not-visible" {
				if code != 1 || tokens != 0 {
					t.Fatalf("first code=%d tokens=%d", code, tokens)
				}
				code, out, diag = runGitManagement(args)
				if mode == "not-visible" {
					if code != 1 {
						t.Fatalf("second code=%d", code)
					}
					saved, err := authclient.NewFilePendingCommandStore(env.pendingPath).Load()
					if err != nil || saved.InstanceID != testInstanceID {
						t.Fatal("transient read discarded accepted creation")
					}
					code, out, diag = runGitManagement(args)
				}
			}
			if mode == "failed" || mode == "wrong-operation" {
				if code != 1 || tokens != 0 || out != "" {
					t.Fatalf("failure code=%d tokens=%d", code, tokens)
				}
				pending, err := authclient.NewFilePendingCommandStore(env.pendingPath).Load()
				if err != nil || pending.InstanceID != testInstanceID || pending.Step != "instance" {
					t.Fatal("lost accepted creation")
				}
			} else if code != 0 || tokens != 1 || !strings.Contains(out, "tiana://") {
				t.Fatalf("code=%d tokens=%d diag=%s", code, tokens, diag)
			}
			if creates != 1 || polls == 0 || gets == 0 {
				t.Fatalf("creates=%d polls=%d gets=%d", creates, polls, gets)
			}
		})
	}
}
