package authclient

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEndpointTokenLookupScopeAndSelection(t *testing.T) {
	now := time.Now().UTC()
	path := filepath.Join(t.TempDir(), "tokens.json")
	base := InstanceTokenCredential{TenantID: "tenant", InstanceID: "sqlite-one", EndpointID: "ep-one", TokenID: "old", Token: "old-secret", ExpiresAt: -1, SavedAt: now}
	save := func(origin string, c InstanceTokenCredential) {
		t.Helper()
		if _, err := NewFileInstanceTokenStore(path, origin).Save(c); err != nil {
			t.Fatal(err)
		}
	}
	save("https://mgr.example.test", base)
	newest := base
	newest.TokenID = "new"
	newest.Token = "new-secret"
	newest.SavedAt = now.Add(time.Second)
	save("https://mgr.example.test", newest)
	expired := newest
	expired.TokenID = "expiring"
	expired.SavedAt = now.Add(time.Hour)
	expired.ExpiresAt = now.Add(30 * time.Second).Unix()
	save("https://mgr.example.test", expired)
	other := newest
	other.EndpointID = "ep-other"
	other.TokenID = "other"
	other.Token = "other-secret"
	save("https://mgr.example.test", other)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"", "https://mgr.example.test/"} {
		got, err := LookupEndpointToken(path, origin, "ep-one", now)
		if err != nil || got.Token != "new-secret" || got.InstanceID != "sqlite-one" {
			t.Fatalf("wrong endpoint Token: id=%s err=%v", got.TokenID, err)
		}
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("lookup mutated store")
	}
	if _, err = LookupEndpointToken(path, "https://wrong.example.test", "ep-one", now); !errors.Is(err, ErrInstanceTokenNotFound) {
		t.Fatal("cross-origin fallback", err)
	}
	save("https://other.example.test", base)
	if _, err = LookupEndpointToken(path, "", "ep-one", now); err == nil {
		t.Fatal("ambiguous origin accepted")
	}
	if _, err = LookupEndpointToken(path, "https://mgr.example.test", "ep-one", now); err != nil {
		t.Fatal("explicit origin not applied", err)
	}
}

func TestEndpointTokenLookupRejectsAmbiguityAndUnsafeFiles(t *testing.T) {
	for _, mode := range []string{"tenant", "instance", "key", "expired", "legacy", "symlink", "public", "corrupt", "missing-endpoint"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tokens.json")
			origin := "https://mgr.example.test"
			now := time.Now()
			c := InstanceTokenCredential{TenantID: "tenant", InstanceID: "sqlite-one", EndpointID: "ep-one", TokenID: "one", Token: "PRIVATE_ENDPOINT_TOKEN", ExpiresAt: -1}
			store := NewFileInstanceTokenStore(path, origin)
			if mode == "expired" {
				c.ExpiresAt = now.Add(-time.Second).Unix()
			}
			if mode == "missing-endpoint" {
				c.EndpointID = ""
			}
			if _, err := store.Save(c); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "tenant", "instance":
				if mode == "tenant" {
					c.TenantID = "other"
				} else {
					c.InstanceID = "sqlite-other"
				}
				c.TokenID = "two"
				c.ExpiresAt = 0
				if _, err := store.Save(c); err != nil {
					t.Fatal(err)
				}
			case "key":
				data, _ := os.ReadFile(path)
				data = bytes.Replace(data, []byte(`"token_id": "one"`), []byte(`"token_id": "mismatch"`), 1)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "legacy":
				data, _ := os.ReadFile(path)
				data = bytes.Replace(data, []byte(`"expires_at": -1`), []byte(`"expires_at": "9999-12-31T23:59:59.999Z"`), 1)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(path, path+".link"); err != nil {
					t.Fatal(err)
				}
				path += ".link"
			case "public":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(path, []byte("broken PRIVATE_ENDPOINT_TOKEN"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			token, err := LookupEndpointToken(path, origin, "ep-one", now)
			if mode == "legacy" {
				if err != nil || token.Token != c.Token {
					t.Fatalf("legacy format rejected: %v", err)
				}
				return
			}
			if err == nil || strings.Contains(err.Error(), "PRIVATE_ENDPOINT_TOKEN") {
				t.Fatalf("unsafe lookup result: %v", err)
			}
		})
	}
}
