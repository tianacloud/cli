package authclient

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestBranchMutationRefreshPinsRequest(t *testing.T) {
	for _, action := range []string{"create", "delete"} {
		t.Run(action, func(t *testing.T) {
			writes, refreshes := 0, 0
			var firstBody []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/auth/refresh" {
					refreshes++
					io.WriteString(w, `{"access_token":"fresh","refresh_token":"fresh-refresh","token_type":"Bearer","expires_in":3600}`)
					return
				}
				writes++
				path, method := "/api/v1/instances/inst_test/branches/child", "DELETE"
				if action == "create" {
					path += "/children"
					method = "POST"
				}
				if r.URL.Path != path || r.Method != method || r.Header.Get("Idempotency-Key") != "stable-branch-key" {
					t.Errorf("wrong request %s %s", r.Method, r.URL)
				}
				body, _ := io.ReadAll(r.Body)
				if writes == 1 {
					firstBody = body
					w.WriteHeader(401)
					io.WriteString(w, `{"error":"expired_access"}`)
					return
				}
				if !bytes.Equal(body, firstBody) || r.Header.Get("Authorization") != "Bearer fresh" {
					t.Error("refresh changed request")
				}
				w.WriteHeader(202)
				io.WriteString(w, `{"instance_id":"inst_test","operation_id":"op-branch"}`)
			}))
			defer server.Close()
			store := NewFileStore(filepath.Join(t.TempDir(), "credentials.json"), server.URL)
			if err := store.Save(Credential{AccessToken: "stale", RefreshToken: "old-refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			client := testClient(t, server, store, new(bytes.Buffer))
			var receipt BranchOperationReceipt
			var err error
			if action == "create" {
				receipt, err = client.CreateBranch(context.Background(), "inst_test", "child", "preview", "stable-branch-key")
			} else {
				receipt, err = client.DeleteBranch(context.Background(), "inst_test", "child", "stable-branch-key")
			}
			if err != nil || receipt.OperationID != "op-branch" || writes != 2 || refreshes != 1 {
				t.Fatalf("receipt=%+v err=%v writes=%d refresh=%d", receipt, err, writes, refreshes)
			}
		})
	}
}
