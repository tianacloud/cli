package appbootstrap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPreviewFilesUseBoundProjectAndHeaderGrant(t *testing.T) {
	content := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer file-grant" || r.Header.Get("Cookie") != "" {
			t.Error("download did not use isolated file grant")
		}
		if r.Header.Get("Range") != "bytes=0-4" {
			t.Error("Range was lost")
		}
		w.Header().Set("Content-Range", "bytes 0-4/9")
		w.WriteHeader(206)
		_, _ = w.Write([]byte("hello"))
	}))
	defer content.Close()
	s := previewFixture(t, nil)
	calls := []string{}
	identity := Identity{ID: "owner", Connection: func(context.Context) (Connection, error) { return Connection{}, nil }, FileHTTP: content.Client(), FileRequest: func(ctx context.Context, method, path string, body, result any) error {
		calls = append(calls, method+" "+path)
		base := "/api/v1/web-projects/" + s.config.Build.Manifest.WebID + "/file-storage/"
		if !strings.HasPrefix(path, base) {
			t.Fatalf("wrong scope %s", path)
		}
		if method == "GET" && body != nil {
			t.Fatal("GET must have no body")
		}
		reply := `{"items":[]}`
		if strings.HasSuffix(path, "read-grant") {
			raw, _ := json.Marshal(map[string]any{"token": "file-grant", "origin": content.URL, "root": "/users/tenant/files/web/", "scope_id": "scope", "expires_at": time.Now().Add(time.Minute).Unix()})
			reply = string(raw)
		}
		return json.Unmarshal([]byte(reply), result)
	}}
	link, err := s.AuthorizeLocalAccount(identity)
	if err != nil {
		t.Fatal(err)
	}
	req := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:4174/web/billing/_tiana/"+path, strings.NewReader(body))
		r.Header.Set("Origin", "http://127.0.0.1:4174")
		r.Header.Set("X-Tiana-Bootstrap", "1")
		r.Header.Set("Range", "bytes=0-4")
		if path == "local-login" {
			r.Header.Set("X-Tiana-Launch", strings.Split(link, "#tiana_launch=")[1])
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if w := req("GET", "file-storage/list?path=docs%2F", "", nil); w.Code != 401 {
		t.Fatalf("anonymous=%d", w.Code)
	}
	login := req("POST", "local-login", "", nil)
	if login.Code != 200 {
		t.Fatalf("login %d %s", login.Code, login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	if w := req("GET", "file-storage/list?path=docs%2F", "", cookie); w.Code != 200 {
		t.Fatalf("list %d %s", w.Code, w.Body.String())
	}
	if !strings.HasSuffix(calls[0], "/list?path=docs%2F") {
		t.Fatal("directory query lost")
	}
	if w := req("DELETE", "file-storage/object?path=docs%2Fa.txt", `{"request_id":"delete-one"}`, cookie); w.Code != 200 {
		t.Fatalf("delete %d %s", w.Code, w.Body.String())
	}
	if w := req("GET", "file-storage/content?path=a.txt", "", cookie); w.Code != 206 || w.Body.String() != "hello" {
		t.Fatalf("content %d %s", w.Code, w.Body.String())
	}
	req("POST", "logout", "", cookie)
	if w := req("GET", "file-storage/content?path=a.txt", "", cookie); w.Code != 401 {
		t.Fatalf("logged out=%d", w.Code)
	}
}
