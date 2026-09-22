package authclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestCredentialCandidatesSendOnlyIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/instances/inst/endpoints/ep/credential-candidates" {
			t.Fatalf("request %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"token_ids":["old","new"]}` {
			t.Fatalf("body=%s", body)
		}
		io.WriteString(w, `{"token_ids":["new"]}`)
	}))
	defer server.Close()
	store := NewFileStore(t.TempDir()+"/accounts.json", server.URL)
	if err := store.Save(Credential{AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	client, err := NewWithConfig(Config{Origin: server.URL, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.CredentialCandidates(context.Background(), "inst", "ep", []string{"old", "new"})
	if err != nil || !reflect.DeepEqual(got, []string{"new"}) {
		t.Fatalf("got=%v err=%v", got, err)
	}
}
