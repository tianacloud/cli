package authclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestPendingLoginPrivateExpiryDenialAndLogout(t *testing.T) {
	for _, outcome := range []string{"expired", "denied", "logout"} {
		t.Run(outcome, func(t *testing.T) {
			polls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/auth/transactions":
					io.WriteString(w, `{"transaction_id":"at-test","client_secret":"private-transaction","user_code":"ABCD-EFGH","verification_uri_complete":"https://console.example/api/v1/a/ABCD-EFGH","expires_in":600,"poll_interval":5}`)
				case "/api/v1/auth/transactions/at-test/poll":
					polls++
					io.WriteString(w, `{"status":"denied"}`)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
			}))
			defer server.Close()
			store := NewFileStore(filepath.Join(t.TempDir(), "credentials.json"), server.URL)
			client := testClient(t, server, store, &bytes.Buffer{})
			ctx := context.Background()
			transaction, err := client.StartLogin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			path, _ := client.pendingLoginPath()
			info, err := os.Stat(path)
			if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
				t.Fatal("pending login is not private")
			}
			other, err := NewWithConfig(Config{Origin: "https://other.example", Store: NewFileStore(store.Path, "https://other.example")})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := other.ResumeLogin(ctx); !errors.Is(err, ErrLoginNotStarted) {
				t.Fatal("origin crossed")
			}
			switch outcome {
			case "expired":
				client.config.Now = func() time.Time { return transaction.CreatedAt.Add(601 * time.Second) }
				if _, err := client.ResumeLogin(ctx); !errors.Is(err, ErrTransactionExpired) {
					t.Fatalf("error=%v", err)
				}
			case "denied":
				if _, err := client.ResumeLogin(ctx); !errors.Is(err, ErrTransactionDenied) {
					t.Fatalf("error=%v", err)
				}
			case "logout":
				if err := client.Logout(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("terminal login state was retained")
			}
			if _, err := client.ResumeLogin(ctx); !errors.Is(err, ErrLoginNotStarted) {
				t.Fatal("cleared login could resume")
			}
			if outcome != "denied" && polls != 0 {
				t.Fatal("invalid login was polled")
			}
		})
	}
}

func TestPendingLoginNeverReplaysInterruptedExchange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("interrupted exchange was replayed") }))
	defer server.Close()
	client := testClient(t, server, NewFileStore(filepath.Join(t.TempDir(), "credentials.json"), server.URL), &bytes.Buffer{})
	err := client.savePendingLogin(pendingLogin{Transaction: AuthTransaction{ID: "at-test", CreatedAt: time.Now(), ExpiresIn: 600}, ClientSecret: "secret", Exchanging: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ResumeLogin(context.Background()); !errors.Is(err, ErrTransactionCompleted) {
		t.Fatalf("error=%v", err)
	}
}
