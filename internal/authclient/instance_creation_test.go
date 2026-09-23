package authclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInstanceCreationReceiptFormats(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		job        uint64
		operation  string
		valid      bool
	}{
		{"queued", `{"instance_id":"inst_test","job_id":17,"operation_id":""}`, 202, 17, "", true},
		{"queued-omitted-operation", `{"instance_id":"inst_test","job_id":17}`, 202, 17, "", true},
		{"submitted", `{"instance_id":"inst_test","job_id":18,"operation_id":"op-test"}`, 202, 18, "op-test", true},
		{"legacy-operation", `{"instance_id":"inst_test","operation_id":"op-test"}`, 202, 0, "op-test", true},
		{"legacy-instance", `{"id":"inst_test","engine":"git"}`, 201, 0, "", true},
		{"uint64-job", `{"instance_id":"inst_test","job_id":18446744073709551615}`, 202, ^uint64(0), "", true},
		{"missing-instance", `{"job_id":17}`, 202, 0, "", false},
		{"missing-tracker", `{"instance_id":"inst_test"}`, 202, 0, "", false},
		{"zero-job", `{"instance_id":"inst_test","job_id":0}`, 202, 0, "", false},
		{"negative-job", `{"instance_id":"inst_test","job_id":-1}`, 202, 0, "", false},
		{"fraction-job", `{"instance_id":"inst_test","job_id":1.5}`, 202, 0, "", false},
		{"string-job", `{"instance_id":"inst_test","job_id":"17"}`, 202, 0, "", false},
		{"overflow-job", `{"instance_id":"inst_test","job_id":18446744073709551616}`, 202, 0, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.Path != "/api/v1/instances" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			got, err := testAuthenticatedClient(t, server).CreateInstance(context.Background(), CreateInstanceRequest{Engine: "git", DisplayName: "demo"}, "stable")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if tc.valid && (got.ID != "inst_test" || got.CurrentJobID != tc.job || got.CreationOperationID != tc.operation) {
				t.Fatalf("receipt=%+v", got)
			}
			if calls != 1 {
				t.Fatalf("requests=%d", calls)
			}
		})
	}
}
