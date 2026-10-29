package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebFunctionsSetUsesExpectedRevision(t *testing.T) {
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.URL.Path != "/api/v1/web-projects/orders/functions" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(404)
			return
		}
		puts++
		var body struct {
			FunctionInstanceIDs []string `json:"function_instance_ids"`
			ExpectedRevision    uint64   `json:"expected_revision"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ExpectedRevision != 7 || strings.Join(body.FunctionInstanceIDs, ",") != "fn-api,fn-admin" {
			t.Errorf("body %+v %v", body, err)
		}
		w.Write([]byte(`{"web_id":"orders","function_instance_ids":["fn-api","fn-admin"],"project_revision":8,"can_manage":true}`))
	}))
	defer server.Close()
	env := newTestEnv(t, server.URL)
	saveTestCredential(t, server.URL, env.credentialsPath, "usr_function")
	var out, diag bytes.Buffer
	code := runCLI(context.Background(), []string{"web", "functions", "set", "orders", "--function-id", "fn-api", "--function-id", "fn-admin", "--expected-revision", "7", "--json"}, strings.NewReader(""), &out, &diag)
	if code != 0 || puts != 1 || !strings.Contains(out.String(), `"project_revision":8`) {
		t.Fatal(code, out.String(), diag.String())
	}
}
