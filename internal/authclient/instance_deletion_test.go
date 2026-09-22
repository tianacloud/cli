package authclient

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestDeleteInstanceRefreshKeepsTargetAndKey(t *testing.T) {
	deletes, refreshes := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/refresh" {
			refreshes++
			fmt.Fprint(w, `{"access_token":"new-access","refresh_token":"new-refresh","token_type":"Bearer","expires_in":3600}`)
			return
		}
		if r.Method != "DELETE" || r.URL.Path != "/api/v1/instances/inst_x" || r.Header.Get("Idempotency-Key") != "delete-stable" {
			t.Error("target or key changed")
		}
		deletes++
		if deletes == 1 {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":{"code":"AUTHENTICATION_REQUIRED"}}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer new-access" {
			t.Error("refresh not used")
		}
		w.WriteHeader(202)
		fmt.Fprint(w, `{"instance_id":"inst_x","operation_id":"op-delete"}`)
	}))
	defer server.Close()
	c := testAuthenticatedClient(t, server)
	receipt, err := c.DeleteInstance(context.Background(), "inst_x", "delete-stable")
	if err != nil || receipt.OperationID != "op-delete" || deletes != 2 || refreshes != 1 {
		t.Fatalf("receipt=%+v err=%v deletes=%d refreshes=%d", receipt, err, deletes, refreshes)
	}
}

func TestDeleteInstanceRejectsUnsafePathBeforeRequest(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(202) }))
	defer server.Close()
	c := testAuthenticatedClient(t, server)
	for _, id := range []string{"", " ", ".", "..", "a/b", "a\\b", "a\nb"} {
		if _, err := c.DeleteInstance(context.Background(), id, "key"); err == nil {
			t.Errorf("accepted unsafe ID %q", id)
		}
	}
	if _, err := c.DeleteInstance(context.Background(), "inst_x", ""); err == nil {
		t.Error("accepted empty key")
	}
	if calls != 0 {
		t.Fatal("unsafe request sent")
	}
}

func TestDeleteInstanceLostResponseIsNotSuccess(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" || r.URL.Path != "/api/v1/instances/inst_x" || r.Header.Get("Idempotency-Key") != "stable" {
			t.Error("wrong deletion identity")
		}
		calls.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	defer server.Close()
	c := testAuthenticatedClient(t, server)
	if _, err := c.DeleteInstance(context.Background(), "inst_x", "stable"); err == nil {
		t.Fatal("lost receipt reported as success")
	}
	if calls.Load() != 1 {
		t.Fatalf("unexpected command-level retry: %d", calls.Load())
	}
}
