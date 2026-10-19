package webtemplate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
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

func TestSharedSourceDownloadAndBounds(t *testing.T) {
	data := zipFixture(t, map[string]string{"metadata.json": `{"name":"notes"}`, "app.js": "demo"})
	sum := sha256.Sum256(data)
	for _, mode := range []string{"valid", "oversized descriptor", "long body", "redirect", "wrong digest"} {
		t.Run(mode, func(t *testing.T) {
			downloads, followed := 0, 0
			object := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				downloads++
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
				if r.URL.Path != "/api/v1/shared-templates/a1b2c3d4e5f6/source" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				size := len(data)
				digest := hex.EncodeToString(sum[:])
				if mode == "oversized descriptor" {
					size = maxSourceZIP + 1
				}
				if mode == "wrong digest" {
					digest = strings.Repeat("0", 64)
				}
				json.NewEncoder(w).Encode(map[string]any{"name": "notes", "url": object.URL + "/source.zip?secret=SIGNED_SECRET", "size": size, "sha256": digest, "prompt": "build my notes"})
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
			result, err := (Client{Auth: auth, DownloadHTTP: downloader}).InitShare(t.Context(), mgr.URL+"/shared-templates/a1b2c3d4e5f6", target)
			if mode == "valid" {
				if err != nil || result.Prompt != "build my notes" {
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
			if mode == "oversized descriptor" && downloads != 0 {
				t.Fatal("download started before size admission")
			}
			if followed != 0 {
				t.Fatal("followed object redirect")
			}
		})
	}
}

func TestSharePublishUsesDirectUploadAndReturnsJobReceipt(t *testing.T) {
	data := []byte("reviewed install archive")
	uploaded, completed := false, false
	object := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("account credential sent to storage")
		}
		if err := r.ParseMultipartForm(1024); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer file.Close()
		body, err := io.ReadAll(file)
		if err != nil || string(body) != string(data) || r.FormValue("key") != "tenant/templates/web/uploads/7/install.zip" {
			t.Errorf("upload body=%q err=%v", body, err)
		}
		uploaded = true
		w.WriteHeader(204)
	}))
	defer object.Close()
	mgr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer account-token" {
			t.Error("missing MGR credential")
		}
		switch r.URL.Path {
		case "/api/v1/web-projects/a1b2c3d4e5f6/share-template/uploads":
			var request struct {
				Size      int64  `json:"size"`
				RequestID string `json:"request_id"`
			}
			json.NewDecoder(r.Body).Decode(&request)
			if request.Size != int64(len(data)) || request.RequestID == "" {
				t.Errorf("request=%+v", request)
			}
			json.NewEncoder(w).Encode(map[string]any{"job_id": 7, "status": "uploading", "form": map[string]any{"url": object.URL, "fields": map[string]string{"key": "tenant/templates/web/uploads/7/install.zip"}}})
		case "/api/v1/web-projects/a1b2c3d4e5f6/share-template/uploads/7/complete":
			if !uploaded {
				t.Error("completed before direct upload")
			}
			completed = true
			json.NewEncoder(w).Encode(map[string]any{"job_id": 7, "status": "pending"})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
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
	file := filepath.Join(t.TempDir(), "install.zip")
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := (Client{Auth: auth}).PublishShare(t.Context(), "a1b2c3d4e5f6", file)
	if err != nil || result.JobID != 7 || result.Status != "pending" || result.Form != nil || !completed {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
