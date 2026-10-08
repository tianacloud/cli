package authclient

import (
	"context"
	"github.com/tianacloud/cli/internal/testutil/account"
	"os"
	"testing"
	"time"
)

func unsetOriginEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"TIANA_API_ORIGIN", "TIANA_MGR_ORIGIN", "TIANA_AUTH_ORIGIN", "TIANA_TOKEN", "TIANA_TOKEN_FILE"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDefaultOriginSelectsOnlineAccount(t *testing.T) {
	const online = "https://console.tianacloud.com"
	const staging = "https://console.tianacloud-staging.net"
	for _, tc := range []struct {
		name  string
		saved []string
	}{{"empty", nil}, {"staging-only", []string{staging}}, {"online-only", []string{online}}, {"both", []string{staging, online}}} {
		t.Run(tc.name, func(t *testing.T) {
			saved := tc.saved
			unsetOriginEnvironment(t)
			path := account.CredentialPath(t)
			hasOnline := false
			for _, origin := range saved {
				token := "staging-access"
				if origin == online {
					token = "online-access"
					hasOnline = true
				}
				if err := NewFileStore(path, origin).Save(Credential{AccessToken: token, RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(path)
			got, err := ResolveOrigin(context.Background())
			if err != nil || got != online {
				t.Fatalf("origin=%q err=%v", got, err)
			}
			token, err := ConnectionCredential(context.Background())
			if hasOnline {
				if err != nil || string(token) != "online-access" {
					t.Fatalf("wrong account: %v", err)
				}
			} else if err == nil {
				t.Fatal("missing online account should require login")
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("account selection changed credentials")
			}
		})
	}
}

func TestExplicitOriginSelectsAccount(t *testing.T) {
	unsetOriginEnvironment(t)
	path := account.CredentialPath(t)
	for _, origin := range []string{"https://first.example.test", "https://second.example.test"} {
		if err := NewFileStore(path, origin).Save(Credential{AccessToken: origin, RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, origin := range []string{"https://first.example.test", "https://second.example.test"} {
		t.Setenv("TIANA_API_ORIGIN", "  "+origin+"  ")
		got, err := ResolveOrigin(context.Background())
		if err != nil || got != origin {
			t.Fatalf("origin=%q err=%v", got, err)
		}
		token, err := ConnectionCredential(context.Background())
		if err != nil || string(token) != origin {
			t.Fatalf("wrong account: %v", err)
		}
	}
}

func TestExplicitEmptyOriginRequiresSelection(t *testing.T) {
	t.Setenv("TIANA_API_ORIGIN", " ")
	if got, err := ResolveOrigin(context.Background()); err == nil || got != "" {
		t.Fatalf("origin=%q err=%v", got, err)
	}
}
