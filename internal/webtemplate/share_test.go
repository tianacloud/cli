package webtemplate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

func fixtureShareClient(t *testing.T, handler http.HandlerFunc) (Client, string) {
	t.Helper()
	mgr := httptest.NewServer(handler)
	t.Cleanup(mgr.Close)
	store := authclient.NewFileStore(filepath.Join(t.TempDir(), "auth.json"), mgr.URL)
	if err := store.Save(authclient.Credential{AccessToken: "account-token", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	auth, err := authclient.NewWithConfig(authclient.Config{Origin: mgr.URL, Store: store, HTTPClient: mgr.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return Client{Auth: auth}, mgr.URL
}

func TestSharedSourceDownloadIntegrity(t *testing.T) {
	data := zipFixture(t, map[string]string{"metadata.json": `{"name":"notes"}`, "app.js": "demo"})
	sum := sha256.Sum256(data)
	for _, mode := range []string{"valid", "market link", "long body", "redirect", "wrong digest"} {
		t.Run(mode, func(t *testing.T) {
			followed := 0
			object := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("account credentials reached object storage")
				}
				if r.URL.Path == "/redirected" {
					followed++
					w.Write(data)
					return
				}
				if mode == "redirect" {
					http.Redirect(w, r, "/redirected", 302)
					return
				}
				w.Write(data)
				if mode == "long body" {
					w.Write([]byte("extra"))
				}
			}))
			defer object.Close()
			mgr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/template-catalog/tpl-notes/source" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				wantChannel := "share"
				if mode == "market link" {
					wantChannel = "market"
				}
				if r.URL.Query().Get("channel") != wantChannel {
					t.Errorf("channel=%q want=%q", r.URL.Query().Get("channel"), wantChannel)
				}
				size := len(data)
				digest := hex.EncodeToString(sum[:])
				if mode == "wrong digest" {
					digest = strings.Repeat("0", 64)
				}
				json.NewEncoder(w).Encode(map[string]any{
					"template_id":   "tpl-notes",
					"source_web_id": "a1b2c3d4e5f6",
					"version":       "2",
					"name":          "notes",
					"url":           object.URL + "/source.zip?secret=SIGNED_SECRET",
					"size":          size,
					"sha256":        digest,
					"prompt":        "build my notes",
				})
			}))
			defer mgr.Close()
			store := authclient.NewFileStore(filepath.Join(t.TempDir(), "auth.json"), mgr.URL)
			if err := store.Save(authclient.Credential{AccessToken: "account-token", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			auth, err := authclient.NewWithConfig(authclient.Config{Origin: mgr.URL, Store: store, HTTPClient: mgr.Client()})
			if err != nil {
				t.Fatal(err)
			}
			downloader := object.Client()
			downloader.Jar, _ = cookiejar.New(nil)
			u, _ := url.Parse(object.URL)
			downloader.Jar.SetCookies(u, []*http.Cookie{{Name: "session", Value: "private"}})
			target := filepath.Join(t.TempDir(), "notes")
			link := mgr.URL + "/templates/tpl-notes"
			if mode == "market link" {
				link += "?channel=market"
			}
			result, err := (Client{Auth: auth, DownloadHTTP: downloader}).InitShare(t.Context(), link, target)
			if mode == "valid" || mode == "market link" {
				if err != nil || result.Prompt != "build my notes" || result.TemplateID != "tpl-notes" {
					t.Fatalf("%+v %v", result, err)
				}
				if body, e := os.ReadFile(filepath.Join(target, "app.js")); e != nil || string(body) != "demo" {
					t.Fatalf("source %q %v", body, e)
				}
			} else {
				if err == nil || strings.Contains(err.Error(), "SIGNED_SECRET") {
					t.Fatalf("failure %v", err)
				}
				if _, e := os.Stat(target); !os.IsNotExist(e) {
					t.Fatalf("partial target %v", e)
				}
			}
			if followed != 0 {
				t.Fatal("followed object redirect")
			}
		})
	}
}

func TestShareLinkRejectsLegacyPath(t *testing.T) {
	calls := 0
	client, origin := fixtureShareClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(404)
	})
	target := filepath.Join(t.TempDir(), "legacy-project")
	_, err := client.InitShare(t.Context(), origin+"/shared-templates/legacy-web", target)
	if err == nil || calls != 0 {
		t.Fatalf("expected legacy link rejection before network: err=%v calls=%d", err, calls)
	}
	if _, e := os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("created target for rejected legacy link")
	}
}
