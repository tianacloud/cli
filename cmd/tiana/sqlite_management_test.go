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
	"os"
	"strings"
	"testing"

	"github.com/tianacloud/cli/internal/authclient"
)

func sqliteInstanceResponse() string {
	return instanceResponse(testInstanceID, "sqlite-db", testEndpointID)
}

func TestSQLiteManagementHelpAndArguments(t *testing.T) {
	for _, command := range [][]string{{"create"}, {"list"}, {"show"}} {
		t.Run(strings.Join(command, "-"), func(t *testing.T) {
			var out, diag bytes.Buffer
			args := append(append([]string{}, command...), "--help")
			if code := runSQLite(context.Background(), args, strings.NewReader(""), &out, &diag); code != 0 {
				t.Fatalf("code=%d error=%s", code, &diag)
			}
			if !strings.Contains(out.String(), "tiana sqlite "+strings.Join(command, " ")) || strings.Contains(out.String(), "tiana db ") {
				t.Fatalf("wrong usage: %s", &out)
			}
		})
	}
	for _, args := range [][]string{{"create"}, {"create", "name", "--engine", "git"}, {"create", "name", "--engine=sqld"}, {"show"}, {"list", "--bad"}, {"tokens", "create"}, {"tokens", "delete"}} {
		var out, diag bytes.Buffer
		if code := runSQLite(context.Background(), args, strings.NewReader(""), &out, &diag); code != 2 {
			t.Fatalf("args=%v code=%d error=%s", args, code, &diag)
		}
		if !strings.Contains(diag.String(), "tiana sqlite ") || strings.Contains(diag.String(), "tiana db ") {
			t.Fatalf("wrong error usage for %v: %s", args, &diag)
		}
	}
}

func TestSQLiteManagementRejectsOtherEngines(t *testing.T) {
	for _, engine := range []string{"git", "sqld", ""} {
		for _, command := range [][]string{{"show", testInstanceID, "--url"}} {
			t.Run(engine+strings.Join(command, "-"), func(t *testing.T) {
				writes := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						writes++
						http.Error(w, "unexpected write", 500)
						return
					}
					fmt.Fprintf(w, `{"id":%q,"engine":%q}`, testInstanceID, engine)
				}))
				defer server.Close()
				env := newTestEnv(t, server.URL)
				saveTestCredential(t, server.URL, env.credentialsPath, "usr_sqlite")
				var out, diag bytes.Buffer
				if code := runSQLite(context.Background(), command, strings.NewReader(""), &out, &diag); code != 1 || !strings.Contains(diag.String(), "engine must be sqlite") {
					t.Fatalf("code=%d error=%s", code, &diag)
				}
				if writes != 0 || out.Len() != 0 {
					t.Fatal("wrong-engine operation produced output or a write")
				}
				if _, err := authclient.NewFilePendingCommandStore(env.pendingPath).Load(); !errors.Is(err, authclient.ErrPendingNotFound) {
					t.Fatalf("rejected fresh operation left pending state: %v", err)
				}
			})
		}
	}
}

func TestSQLiteListFiltersEveryPageWithoutUnsupportedQuery(t *testing.T) {
	pages := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		q := r.URL.Query()
		if r.URL.Path != "/api/v1/instances" || len(q) != 2 || q.Get("page") != fmt.Sprint(pages) || q.Get("page_size") != "20" {
			t.Errorf("unexpected query %s", r.URL)
		}
		engine := "git"
		if pages == 3 {
			engine = "sqlite"
		}
		fmt.Fprintf(w, `{"items":[{"id":"instance-%d","display_name":"name-%d","engine":%q}],"page":%d,"page_size":20,"total":41,"total_pages":3}`, pages, pages, engine, pages)
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_sqlite")
	var out, diag bytes.Buffer
	if code := runSQLite(context.Background(), []string{"list"}, strings.NewReader(""), &out, &diag); code != 0 {
		t.Fatalf("code=%d error=%s", code, &diag)
	}
	if pages != 3 || !strings.Contains(out.String(), "instance-3") || strings.Contains(out.String(), "instance-1") || strings.Contains(out.String(), "instance-2") {
		t.Fatalf("pages=%d output=%s", pages, &out)
	}
}

func TestSQLiteListFiltersAcrossServerPages(t *testing.T) {
	pages := 0
	fetch := func(_ context.Context, page, size int) (authclient.InstancePage, error) {
		pages++
		engine := "git"
		if page == 2 {
			engine = "sqlite"
		}
		return authclient.InstancePage{Items: []authclient.Instance{{ID: fmt.Sprint(page), DisplayName: fmt.Sprintf("row-%d", page), Engine: engine}}, Page: page, PageSize: size, Total: 2, TotalPages: 2}, nil
	}
	var out bytes.Buffer
	items, err := fetchAllInstancesScoped(context.Background(), fetch, nonInteractivePageSize, sqliteManagementScope)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeInstanceTable(&out, items); err != nil {
		t.Fatal(err)
	}
	if pages != 2 || !strings.Contains(out.String(), "row-2") || strings.Contains(out.String(), "row-1") || strings.Contains(out.String(), "ENGINE") {
		t.Fatalf("pages=%d output=%s", pages, &out)
	}
}

func TestSQLiteManagementShowSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/instances/"+testInstanceID+"/branches/main" {
			io.WriteString(w, sqliteBranchResponse(testInstanceID, "main", "production", testEndpointID))
			return
		}

		switch {
		case r.URL.Path == "/api/v1/instances/"+testInstanceID && r.Method == http.MethodGet:
			io.WriteString(w, sqliteInstanceResponse())
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_sqlite")
	t.Setenv("TIANA_TOKEN", "invalid-unused-SQL-token")
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"show", testInstanceID, "--url"}, "https://" + testEndpointID + ".db.example.test\n"},
	} {
		var out, diag bytes.Buffer
		if code := runSQLite(context.Background(), test.args, strings.NewReader(""), &out, &diag); code != 0 || out.String() != test.want {
			t.Fatalf("args=%v code=%d error=%s; output mismatch=%t", test.args, code, &diag, out.String() != test.want)
		}
	}
	if _, err := os.Stat(env.tokensPath); !os.IsNotExist(err) {
		t.Fatal("unexpected Token cache")
	}
}

func TestSQLiteListLatePageErrorProducesNoPartialOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			fmt.Fprintf(w, `{"items":[%s],"page":1,"page_size":20,"total":21,"total_pages":2}`, sqliteInstanceResponse())
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, `{"error":{"code":"UNAVAILABLE","message":"unavailable"}}`)
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_sqlite")
	var out, diag bytes.Buffer
	if code := runSQLite(context.Background(), []string{"list"}, strings.NewReader(""), &out, &diag); code != 1 || out.Len() != 0 {
		t.Fatalf("code=%d bytes=%d error=%s", code, out.Len(), &diag)
	}
}

func sqliteBranchResponse(instanceID, branchID, name, endpoint string) string {
	detail := authclient.BranchDetail{InstanceID: instanceID, Branch: authclient.Branch{ID: branchID, Name: name, Root: branchID == "main", EndpointID: endpoint, LifecycleState: "AVAILABLE", RuntimeState: "SLEEPING"}}
	if endpoint != "" {
		detail.Connection = &authclient.InstanceConnection{Hostname: endpoint + ".db.example.test", URL: "https://" + endpoint + ".db.example.test"}
	}
	body, _ := json.Marshal(detail)
	return string(body)
}
