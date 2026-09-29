package appbootstrap

import (
	"context"
	"github.com/tianacloud/cli/internal/authclient"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPreviewCarriesBrowserIdentityToManagementRequests(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "req-preview-login"
		if r.URL.Path == "/connection" {
			want = "req-preview-connection"
		}
		if r.Header.Get("X-Request-ID") != want {
			t.Errorf("%s ID=%q want %q", r.URL.Path, r.Header.Get("X-Request-ID"), want)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	defer peer.Close()
	store := authclient.NewFileStore(t.TempDir()+"/credential.json", peer.URL)
	if err := store.Save(authclient.Credential{AccessToken: "fixture", RefreshToken: "fixture", ExpiresAt: time.Now().Add(time.Hour), User: authclient.User{ID: "owner"}}); err != nil {
		t.Fatal(err)
	}
	client, err := authclient.NewWithConfig(authclient.Config{Origin: peer.URL, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	call := func(ctx context.Context, path string) error {
		_, err := client.DoJSON(ctx, "GET", path, nil, nil, nil)
		return err
	}
	s := previewFixture(t, func(ctx context.Context) (LoginFlow, error) {
		if err := call(ctx, "/start"); err != nil {
			return LoginFlow{}, err
		}
		return LoginFlow{VerificationURL: "https://console.example.test/authorize", ExpiresAt: time.Now().Add(time.Minute), Complete: func(ctx context.Context) (Identity, error) {
			if err := call(ctx, "/complete"); err != nil {
				return Identity{}, err
			}
			return Identity{ID: "owner", Connection: func(ctx context.Context) (Connection, error) {
				if err := call(ctx, "/connection"); err != nil {
					return Connection{}, err
				}
				return Connection{InstanceID: "ins_billing", Token: "fixture"}, nil
			}}, nil
		}}, nil
	})
	request := func(path, id string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "http://127.0.0.1:4174/web/billing/_tiana/"+path, nil)
		req.Header.Set("Origin", s.config.Origin)
		req.Header.Set("X-Tiana-Bootstrap", "1")
		req.Header.Set("X-Request-ID", id)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		if w.Header().Get("X-Request-ID") != id {
			t.Errorf("response lost ID")
		}
		return w
	}
	login := request("login", "req-preview-login", nil)
	if login.Code != 202 {
		t.Fatalf("login %d", login.Code)
	}
	cookie := login.Result().Cookies()[0]
	for i := 0; i < 100; i++ {
		if previewRequest(s, "GET", "/web/billing/_tiana/session", cookie).Code == 200 {
			break
		}
		time.Sleep(time.Millisecond * 5)
	}
	if w := request("connection", "req-preview-connection", cookie); w.Code != 200 {
		t.Fatalf("connection %d", w.Code)
	}
}
