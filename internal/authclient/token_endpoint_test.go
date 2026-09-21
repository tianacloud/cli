package authclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateTokenRequiresTargetAndIdempotencyBeforeRequest(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	client := testAuthenticatedClient(t, server)
	for _, target := range [][3]string{{"", "ep-test", "key"}, {"inst_test", "", "key"}, {"inst_test", " \t", "key"}, {"inst_test", "ep-test", ""}} {
		if _, err := client.CreateInstanceToken(context.Background(), target[0], target[1], CreateTokenRequest{}, target[2]); err == nil {
			t.Error("accepted incomplete target or idempotency key")
		}
	}
	if calls != 0 {
		t.Fatalf("invalid target made %d requests", calls)
	}
}
