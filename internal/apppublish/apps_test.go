package apppublish

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"github.com/tianacloud/cli/internal/authclient"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppsCreateReusesCloudAuthentication(t *testing.T) {
	called := false
	r, _, out, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) {
		called = true
		if req.Method != "POST" || req.URL.Path != "/api/v1/web-projects" || req.Header.Get("Authorization") != "Bearer private-access" {
			t.Errorf("unexpected Web request")
		}
		io.WriteString(w, `{"id":"AAAAAAAAAAAA","name":"Billing","owner_id":"prn-test","tenant_id":"ten-test"}`)
	})
	if code := runTest(r, out, context.Background(), []string{"web", "create", "Billing", "--json"}); code != 0 || !called {
		t.Fatalf("Web create failed: %d %s", code, out)
	}
}

func TestGenericMGRFailurePreservesHTTPStatus(t *testing.T) {
	r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":"request failed"}`)
	})

	result := r.Run(t.Context(), Options{Command: "create", Name: "Billing", RequestID: "request-test"})
	if result.Error == nil || !strings.Contains(result.Error.Message, "HTTP 503") {
		t.Fatalf("error=%+v", result.Error)
	}
}

func runnerForTest(t *testing.T, handler http.HandlerFunc) (Runner, string, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	store := authclient.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"), server.URL)
	if err := store.Save(authclient.Credential{AccessToken: "private-access", RefreshToken: "private-refresh", ExpiresAt: time.Now().Add(time.Hour), User: authclient.User{ID: "prn-test"}}); err != nil {
		t.Fatal(err)
	}
	client, err := authclient.NewWithConfig(authclient.Config{Origin: server.URL, Store: store, HTTPClient: server.Client(), NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	return Runner{Client: client}, "prn-test", &bytes.Buffer{}, &bytes.Buffer{}
}
func runTest(r Runner, out *bytes.Buffer, ctx context.Context, args []string) int {
	f := flag.NewFlagSet("test", flag.ContinueOnError)
	o := Options{Command: args[1], RequestID: "test-request"}
	if len(args) > 2 {
		if o.Command == "create" {
			o.Name = args[2]
		} else {
			o.ID = args[2]
		}
	}
	f.StringVar(&o.Dir, "dir", "", "")
	f.Bool("json", false, "")
	if err := f.Parse(args[3:]); err != nil {
		return 2
	}
	result := r.Run(ctx, o)
	json.NewEncoder(out).Encode(result)
	if result.Error != nil {
		return result.Error.ExitCode
	}
	return 0
}
