package authclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateTokenRequiresTargetAndRequestIDBeforeRequest(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	client := testAuthenticatedClient(t, server)
	for _, target := range [][2]string{{"", "ep-test"}, {"inst_test", ""}, {"inst_test", " \t"}} {
		if _, err := client.CreateInstanceToken(context.Background(), target[0], target[1], CreateTokenRequest{RequestID: "request"}); err == nil {
			t.Error("accepted incomplete target")
		}
	}
	if _, err := client.CreateInstanceToken(context.Background(), "inst_test", "ep-test", CreateTokenRequest{}); err == nil {
		t.Error("accepted missing request_id")
	}
	if calls != 0 {
		t.Fatalf("invalid target made %d requests", calls)
	}
}
