package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

const (
	testEndpointID = "ep-0abcdefghjkmnpqrstvwxyz012"
	testInstanceID = "inst_cli"
)

type testEnv struct {
	credentialsPath string
	tokensPath      string
	pendingPath     string
}

func newTestEnv(t *testing.T, serverURL string) testEnv {
	t.Helper()
	directory := t.TempDir()
	env := testEnv{
		credentialsPath: filepath.Join(directory, "credentials.json"),
		tokensPath:      filepath.Join(directory, "instance-tokens.json"),
		pendingPath:     filepath.Join(directory, "pending.json"),
	}
	t.Setenv("TIANA_API_ORIGIN", serverURL)
	t.Setenv("TIANA_CREDENTIALS_FILE", env.credentialsPath)
	t.Setenv("TIANA_INSTANCE_TOKENS_FILE", env.tokensPath)
	t.Setenv("TIANA_PENDING_COMMAND_FILE", env.pendingPath)
	return env
}

func saveTestCredential(t *testing.T, serverURL, path, userID string) {
	t.Helper()
	store := authclient.NewFileStore(path, serverURL)
	if err := store.Save(authclient.Credential{
		AccessToken: "access-" + userID, RefreshToken: "refresh-" + userID, TokenType: "Bearer",
		ExpiresAt: time.Now().Add(time.Hour), User: authclient.User{ID: userID, Email: userID + "@example.com"},
	}); err != nil {
		t.Fatal(err)
	}
}

func instanceResponse(id, name string, endpoint string) string {
	connection := ""
	if endpoint != "" {
		connection = fmt.Sprintf(`,"endpoint_id":"%s","connection":{"hostname":"%s.db.example.test","url":"https://%s.db.example.test"}`, endpoint, endpoint, endpoint)
	}
	return fmt.Sprintf(`{"id":"%s","display_name":"%s","engine":"sqlite","product_state":"ACTIVE"%s}`, id, name, connection)
}

func appTypesResponse(engines ...string) string {
	items := make([]string, 0, len(engines))
	for _, engine := range engines {
		items = append(items, fmt.Sprintf(`{"engine":"%s","generation":"1"}`, engine))
	}
	return `{"items":[` + strings.Join(items, ",") + `]}`
}

func TestSQLiteManagementCreateRequiresNameBeforeAnyRequest(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.NotFound(w, r)
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_create")

	var output, errorOutput bytes.Buffer
	if status := runSQLite(context.Background(), []string{"create"}, strings.NewReader(""), &output, &errorOutput); status != 2 {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, output.String(), errorOutput.String())
	}
	if requests != 0 {
		t.Fatalf("requests=%d, want 0", requests)
	}
	if output.Len() != 0 {
		t.Fatalf("stdout=%q, want empty", output.String())
	}
	for _, want := range []string{"a database name is required", "tiana sqlite create [options] NAME"} {
		if !strings.Contains(errorOutput.String(), want) {
			t.Fatalf("stderr=%q missing %q", errorOutput.String(), want)
		}
	}
}

func TestSQLiteManagementUsageErrorsPrintCommandUsage(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "show", args: []string{"show"}, want: "tiana sqlite show [options] INSTANCE"},
		{name: "list", args: []string{"list", "--unknown"}, want: "tiana sqlite list"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			newTestEnv(t, "http://127.0.0.1:1")
			var output, errorOutput bytes.Buffer
			if status := runSQLite(context.Background(), test.args, strings.NewReader(""), &output, &errorOutput); status != 2 {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, output.String(), errorOutput.String())
			}
			if !strings.Contains(errorOutput.String(), test.want) {
				t.Fatalf("stderr=%q missing %q", errorOutput.String(), test.want)
			}
		})
	}
}

func TestSQLiteManagementCreateRejectsUnknownNameOption(t *testing.T) {
	newTestEnv(t, "http://127.0.0.1:1")
	var output, errorOutput bytes.Buffer
	if status := runSQLite(context.Background(), []string{"create", "--name", "my-db"}, strings.NewReader(""), &output, &errorOutput); status != 2 {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, output.String(), errorOutput.String())
	}
	for _, want := range []string{"invalid options or option value", "tiana sqlite create [options] NAME"} {
		if !strings.Contains(errorOutput.String(), want) {
			t.Fatalf("stderr=%q missing %q", errorOutput.String(), want)
		}
	}
}

func TestSQLiteManagementCreateAcceptsPositionalName(t *testing.T) {
	var requestBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/app-types":
			_, _ = io.WriteString(w, appTypesResponse("sqlite"))
		case "/api/v1/instances":
			raw, _ := io.ReadAll(r.Body)
			requestBody = string(raw)
			_, _ = io.WriteString(w, instanceResponse(testInstanceID, "my-db", testEndpointID))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_display")

	var output, errorOutput bytes.Buffer
	if status := runSQLite(context.Background(), []string{"create", "my-db"}, strings.NewReader(""), &output, &errorOutput); status != 0 {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, output.String(), errorOutput.String())
	}
	if !strings.Contains(requestBody, `"display_name":"my-db"`) {
		t.Fatalf("request body=%q", requestBody)
	}
}

func TestSQLiteManagementCreateRejectsMultipleNames(t *testing.T) {
	newTestEnv(t, "http://127.0.0.1:1")
	var output, errorOutput bytes.Buffer
	if status := runSQLite(context.Background(), []string{"create", "my-db", "other"}, strings.NewReader(""), &output, &errorOutput); status != 2 {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, output.String(), errorOutput.String())
	}
	if !strings.Contains(errorOutput.String(), "provided more than once") || !strings.Contains(errorOutput.String(), "tiana sqlite create [options] NAME") {
		t.Fatalf("stderr=%q", errorOutput.String())
	}
}

func TestSQLiteManagementCreateRejectsUnavailableEngineBeforeWrite(t *testing.T) {
	var createRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/app-types":
			_, _ = io.WriteString(w, appTypesResponse("lite"))
		case "/api/v1/instances":
			createRequests++
			_, _ = io.WriteString(w, instanceResponse(testInstanceID, "my-db", testEndpointID))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_engine")

	var output, errorOutput bytes.Buffer
	status := runSQLite(context.Background(), []string{"create", "my-db"}, strings.NewReader(""), &output, &errorOutput)
	if status != 2 {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, output.String(), errorOutput.String())
	}
	if createRequests != 0 {
		t.Fatalf("create requests=%d, want 0", createRequests)
	}
	if !strings.Contains(errorOutput.String(), `engine "sqlite" is not available; supported engines: lite`) {
		t.Fatalf("stderr=%q", errorOutput.String())
	}
	if _, err := os.Stat(env.pendingPath); !os.IsNotExist(err) {
		t.Fatalf("pending command exists: %v", err)
	}
}

func TestSQLiteManagementCreateDefiniteRejectionDoesNotBlockNextCreate(t *testing.T) {
	var createRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/app-types":
			_, _ = io.WriteString(w, appTypesResponse("sqlite"))
		case "/api/v1/instances":
			createRequests++
			if createRequests == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"code":"INVALID_DISPLAY_NAME","message":"display_name is invalid"}}`)
				return
			}
			_, _ = io.WriteString(w, instanceResponse(testInstanceID, "second", testEndpointID))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_definite")

	var firstOutput, firstError bytes.Buffer
	if status := runSQLite(context.Background(), []string{"create", "first"}, strings.NewReader(""), &firstOutput, &firstError); status != 1 {
		t.Fatalf("first status=%d stdout=%q stderr=%q", status, firstOutput.String(), firstError.String())
	}
	if _, err := os.Stat(env.pendingPath); !os.IsNotExist(err) {
		t.Fatalf("pending command survived a definite rejection: %v", err)
	}

	var secondOutput, secondError bytes.Buffer
	if status := runSQLite(context.Background(), []string{"create", "second"}, strings.NewReader(""), &secondOutput, &secondError); status != 0 {
		t.Fatalf("second status=%d stdout=%q stderr=%q", status, secondOutput.String(), secondError.String())
	}
	if createRequests != 2 {
		t.Fatalf("create requests=%d, want 2", createRequests)
	}
	if !strings.Contains(secondOutput.String(), testInstanceID) {
		t.Fatalf("second stdout=%q", secondOutput.String())
	}
}

func TestSQLiteManagementCreateUnknownOutcomeKeepsPendingWithRecoveryHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/app-types":
			_, _ = io.WriteString(w, appTypesResponse("sqlite"))
		case "/api/v1/instances":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"code":"INTERNAL","message":"temporary failure"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_unknown_outcome")

	var output, errorOutput bytes.Buffer
	if status := runSQLite(context.Background(), []string{"create", "first"}, strings.NewReader(""), &output, &errorOutput); status != 1 {
		t.Fatalf("first status=%d stderr=%q", status, errorOutput.String())
	}
	if _, err := os.Stat(env.pendingPath); err != nil {
		t.Fatalf("pending command missing after unknown outcome: %v", err)
	}

	var blockedOutput, blockedError bytes.Buffer
	if status := runSQLite(context.Background(), []string{"create", "second"}, strings.NewReader(""), &blockedOutput, &blockedError); status != 1 {
		t.Fatalf("blocked status=%d stdout=%q stderr=%q", status, blockedOutput.String(), blockedError.String())
	}
	for _, want := range []string{"must be completed first", "tiana sqlite 'create' 'first'", env.pendingPath} {
		if !strings.Contains(blockedError.String(), want) {
			t.Fatalf("stderr=%q missing %q", blockedError.String(), want)
		}
	}
}

func TestSQLiteManagementShowURLPrintsOnlyURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/instances/"+testInstanceID+"/branches/main" {
			io.WriteString(w, sqliteBranchResponse(testInstanceID, "main", "production", testEndpointID))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/instances/"+testInstanceID {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, instanceResponse(testInstanceID, "my-db", testEndpointID))
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_show")

	var output, errorOutput bytes.Buffer
	if status := runSQLite(context.Background(), []string{"show", testInstanceID, "--url"}, strings.NewReader(""), &output, &errorOutput); status != 0 {
		t.Fatalf("status=%d stderr=%q", status, errorOutput.String())
	}
	want := "https://" + testEndpointID + ".db.example.test\n"
	if output.String() != want {
		t.Fatalf("stdout=%q, want %q", output.String(), want)
	}
}

func TestSQLiteManagementShowURLFailsWhenEndpointIsNotReady(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"inst_pending","display_name":"pending","engine":"sqlite","product_state":"PENDING"}`)
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_show")

	var output, errorOutput bytes.Buffer
	if status := runSQLite(context.Background(), []string{"show", "inst_pending", "--url"}, strings.NewReader(""), &output, &errorOutput); status != 1 {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, output.String(), errorOutput.String())
	}
	if output.Len() != 0 {
		t.Fatalf("stdout=%q, want empty", output.String())
	}
	if !strings.Contains(errorOutput.String(), "no connection URL") {
		t.Fatalf("stderr=%q", errorOutput.String())
	}
}

func TestSQLiteManagementShowMarksStaleRuntimeStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"inst_stale","display_name":"stale","engine":"sqlite","product_state":"ACTIVE","endpoint_id":"`+testEndpointID+`","connection":{"hostname":"`+testEndpointID+`.db.example.test","url":"https://`+testEndpointID+`.db.example.test"},"runtime_status_stale":true,"stale_reason":"CONTROL_UNAVAILABLE","created_at":"2026-09-10T12:00:00.000Z"}`)
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_show")

	var output, errorOutput bytes.Buffer
	if status := runSQLite(context.Background(), []string{"show", "inst_stale"}, strings.NewReader(""), &output, &errorOutput); status != 0 {
		t.Fatalf("status=%d stderr=%q", status, errorOutput.String())
	}
	for _, want := range []string{"stale", "CONTROL_UNAVAILABLE", "https://" + testEndpointID} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("stdout=%q missing %q", output.String(), want)
		}
	}
}

func TestSQLiteManagementListNonInteractiveFetchesEveryPageOnce(t *testing.T) {
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/instances" {
			http.NotFound(w, r)
			return
		}
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		if r.URL.Query().Get("page_size") != "20" {
			t.Errorf("page_size=%q", r.URL.Query().Get("page_size"))
		}
		if page == "1" {
			items := make([]string, 0, 20)
			for index := 1; index <= 20; index++ {
				items = append(items, fmt.Sprintf(`{"id":"inst-%02d","display_name":"db-%02d","engine":"sqlite","product_state":"ACTIVE"}`, index, index))
			}
			_, _ = io.WriteString(w, `{"items":[`+strings.Join(items, ",")+`],"page":1,"page_size":20,"total":25,"total_pages":2}`)
			return
		}
		items := make([]string, 0, 5)
		for index := 21; index <= 25; index++ {
			items = append(items, fmt.Sprintf(`{"id":"inst-%02d","display_name":"db-%02d","engine":"sqlite","product_state":"ACTIVE"}`, index, index))
		}
		_, _ = io.WriteString(w, `{"items":[`+strings.Join(items, ",")+`],"page":2,"page_size":20,"total":25,"total_pages":2}`)
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_list")

	var output, errorOutput bytes.Buffer
	if status := runSQLite(context.Background(), []string{"list"}, strings.NewReader(""), &output, &errorOutput); status != 0 {
		t.Fatalf("status=%d stderr=%q", status, errorOutput.String())
	}
	if len(pages) != 2 || pages[0] != "1" || pages[1] != "2" {
		t.Fatalf("pages=%q", pages)
	}
	if strings.Count(output.String(), "ID ") != 1 || strings.Contains(output.String(), "-- More --") {
		t.Fatalf("stdout=%q", output.String())
	}
	for index := 1; index <= 25; index++ {
		if !strings.Contains(output.String(), fmt.Sprintf("inst-%02d", index)) {
			t.Fatalf("stdout missing inst-%02d: %q", index, output.String())
		}
	}
}

func TestSQLiteManagementListMidPageFailureLeavesNoOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "1" {
			_, _ = io.WriteString(w, `{"items":[{"id":"inst-01","display_name":"db","engine":"sqlite","product_state":"ACTIVE"}],"page":1,"page_size":20,"total":21,"total_pages":2}`)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"code":"INTERNAL","message":"list failed"}}`)
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_list")

	var output, errorOutput bytes.Buffer
	if status := runSQLite(context.Background(), []string{"list"}, strings.NewReader(""), &output, &errorOutput); status != 1 {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, output.String(), errorOutput.String())
	}
	if output.Len() != 0 {
		t.Fatalf("stdout=%q, want empty", output.String())
	}
}

func TestSQLiteManagementListReportsEmptyTenant(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[],"page":1,"page_size":20,"total":0,"total_pages":0}`)
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_list")

	var output, errorOutput bytes.Buffer
	if status := runSQLite(context.Background(), []string{"list"}, strings.NewReader(""), &output, &errorOutput); status != 0 {
		t.Fatalf("status=%d stderr=%q", status, errorOutput.String())
	}
	if !strings.Contains(output.String(), "No databases found.") {
		t.Fatalf("stdout=%q", output.String())
	}
}

func TestSQLiteManagementListRejectsRemovedNonInteractive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/auth") {
			t.Errorf("non-interactive list started authentication: %s", r.URL.Path)
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	newTestEnv(t, server.URL)
	var output, errorOutput bytes.Buffer
	if status := runSQLite(context.Background(), []string{"list", "--non-interactive"}, strings.NewReader(""), &output, &errorOutput); status != 2 {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, output.String(), errorOutput.String())
	}
	if !strings.Contains(errorOutput.String(), "invalid options") {
		t.Fatalf("stderr=%q", errorOutput.String())
	}
}

func TestInteractiveListPagesAndQuits(t *testing.T) {
	var requested []int
	fetch := func(_ context.Context, page, pageSize int) (authclient.InstancePage, error) {
		requested = append(requested, page)
		if page == 1 {
			return authclient.InstancePage{Items: []authclient.Instance{{ID: "inst-1", DisplayName: "one"}, {ID: "inst-2", DisplayName: "two"}}, Page: 1, PageSize: pageSize, Total: 3, TotalPages: 2}, nil
		}
		return authclient.InstancePage{Items: []authclient.Instance{{ID: "inst-3", DisplayName: "three"}}, Page: 2, PageSize: pageSize, Total: 3, TotalPages: 2}, nil
	}
	var output bytes.Buffer
	err := runInteractiveList(context.Background(), fetch, strings.NewReader("n\nq\n"), &output)
	if err != nil {
		t.Fatal(err)
	}
	if len(requested) != 2 || requested[0] != 1 || requested[1] != 2 {
		t.Fatalf("requested pages=%v", requested)
	}
	for _, want := range []string{"inst-1", "inst-2", "inst-3", "-- More --"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output=%q missing %q", output.String(), want)
		}
	}
}

func TestInteractiveListQuitDoesNotRequestNextPage(t *testing.T) {
	var requested []int
	fetch := func(_ context.Context, page, pageSize int) (authclient.InstancePage, error) {
		requested = append(requested, page)
		return authclient.InstancePage{Items: []authclient.Instance{{ID: "inst-1"}}, Page: 1, PageSize: pageSize, Total: 2, TotalPages: 2}, nil
	}
	var output bytes.Buffer
	if err := runInteractiveList(context.Background(), fetch, strings.NewReader("q\n"), &output); err != nil {
		t.Fatal(err)
	}
	if len(requested) != 1 {
		t.Fatalf("requested pages=%v", requested)
	}
}

func TestInteractiveListLastPageDoesNotPrompt(t *testing.T) {
	fetch := func(_ context.Context, page, pageSize int) (authclient.InstancePage, error) {
		return authclient.InstancePage{Items: []authclient.Instance{{ID: "inst-1"}}, Page: page, PageSize: pageSize, Total: 1, TotalPages: 1}, nil
	}
	var output bytes.Buffer
	if err := runInteractiveList(context.Background(), fetch, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "-- More --") {
		t.Fatalf("output=%q", output.String())
	}
}

func TestReadPageCommand(t *testing.T) {
	tests := []struct {
		input string
		next  bool
		quit  bool
	}{
		{input: "n\n", next: true},
		{input: "N\n", next: true},
		{input: " \n", next: true},
		{input: "\n", next: true},
		{input: "q\n", quit: true},
		{input: "Q\n", quit: true},
		{input: "x\n", next: false, quit: false},
	}
	for _, test := range tests {
		reader := bufio.NewReader(strings.NewReader(test.input))
		next, quit, err := readPageCommand(reader)
		if err != nil || next != test.next || quit != test.quit {
			t.Fatalf("input=%q next=%v quit=%v err=%v", test.input, next, quit, err)
		}
	}
}

func TestSQLiteManagementHelpListsResourceCommands(t *testing.T) {
	var rootOutput bytes.Buffer
	runCLI(context.Background(), []string{"--help"}, strings.NewReader(""), &rootOutput, io.Discard)
	rootHelp := rootOutput.String()
	for _, value := range []string{
		"sqlite",
		"SQLite product commands",
	} {
		if !strings.Contains(rootHelp, value) {
			t.Fatalf("root help=%q missing %q", rootHelp, value)
		}
	}
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "db create", args: []string{"create", "--help"}, want: []string{"The sqlite engine is fixed", "NAME is required"}},
		{name: "db show", args: []string{"show", "--help"}, want: []string{"With --url, stdout contains only the connection URL."}},
		{name: "db list", args: []string{"list", "--help"}, want: []string{"interactive terminal pages the results"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output, errorOutput bytes.Buffer
			if status := runSQLite(context.Background(), test.args, strings.NewReader(""), &output, &errorOutput); status != 0 {
				t.Fatalf("status=%d stderr=%q", status, errorOutput.String())
			}
			for _, want := range test.want {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("output=%q missing %q", output.String(), want)
				}
			}
		})
	}
}

func TestSQLiteManagementShowURLLoginOutputGoesToStderr(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/instances/"+testInstanceID+"/branches/main" {
			io.WriteString(w, sqliteBranchResponse(testInstanceID, "main", "production", testEndpointID))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/transactions":
			_, _ = io.WriteString(w, `{"transaction_id":"at_show","client_secret":"secret","user_code":"H7K9-M2PX","verification_uri":"https://auth.example/api/v1/device","verification_uri_complete":"https://auth.example/api/v1/a/H7K9-M2PX","expires_in":600,"poll_interval":0}`)
		case "/api/v1/auth/transactions/at_show/poll":
			_, _ = io.WriteString(w, `{"status":"approved","authorization_code":"code","expires_in":30}`)
		case "/api/v1/auth/token":
			_, _ = io.WriteString(w, `{"access_token":"access","refresh_token":"refresh","token_type":"Bearer","expires_in":3600,"user":{"user_id":"usr_show","email":"show@example.com"}}`)
		case "/api/v1/instances/" + testInstanceID:
			_, _ = io.WriteString(w, instanceResponse(testInstanceID, "my-db", testEndpointID))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	newTestEnv(t, server.URL)

	var output, errorOutput bytes.Buffer
	if status := runSQLite(context.Background(), []string{"show", testInstanceID, "--url"}, strings.NewReader(""), &output, &errorOutput); status != 0 {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, output.String(), errorOutput.String())
	}
	want := "https://" + testEndpointID + ".db.example.test\n"
	if output.String() != want {
		t.Fatalf("stdout=%q, want %q", output.String(), want)
	}
	for _, message := range []string{"You need to authenticate.", "Open:\nhttps://auth.example/api/v1/a/H7K9-M2PX", "Waiting for authentication..."} {
		if !strings.Contains(errorOutput.String(), message) {
			t.Fatalf("stderr=%q missing %q", errorOutput.String(), message)
		}
	}
}
