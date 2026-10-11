package fileclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMultipartUploadStreamsExactTypeAndLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.ContentLength <= 3 {
			t.Error("invalid upload transport")
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(part)
			if part.FormName() == "file" {
				found = true
				if string(data) != "abc" || part.Header.Get("Content-Type") != "text/plain" || part.FileName() != "报告.txt" {
					t.Error("invalid file part")
				}
			}
		}
		if !found {
			t.Error("missing file")
		}
		w.WriteHeader(201)
	}))
	defer server.Close()
	c := Client{HTTP: server.Client()}
	if err := c.Upload(t.Context(), Form{URL: server.URL, Fields: map[string]string{"Content-Type": "text/plain", "key": "t/files/报告.txt", "policy": "signed"}}, "报告.txt", 3, strings.NewReader("abc")); err != nil {
		t.Fatal(err)
	}
}
func TestGrantCacheAndDownloadRefresh(t *testing.T) {
	reads := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		if r.Header.Get("Authorization") != "Bearer file-token" {
			t.Error("missing read grant")
		}
		if reads == 1 {
			w.Header().Set("X-Tiana-File-Error", "file_grant_expired")
			w.WriteHeader(403)
			return
		}
		if r.URL.Path != "/users/t/files/报告 #.txt" || r.URL.Query().Get("_fs_scope") != "scope" {
			t.Error("wrong URL")
		}
		_, _ = io.WriteString(w, "abc")
	}))
	defer server.Close()
	grants := 0
	c := Client{HTTP: server.Client(), Request: func(_ context.Context, method, path string, body, result any) error {
		if method != "POST" || path != "/read-grant" {
			t.Fatal("unexpected management request")
		}
		grants++
		*result.(*Grant) = Grant{Token: "file-token", Origin: server.URL, Root: "/users/t/files/", ScopeID: "scope", ExpiresAt: time.Now().Add(time.Minute).Unix()}
		return nil
	}}
	address, err := c.GetURL(t.Context(), "报告 #.txt")
	if err != nil || strings.Contains(address, "file-token") {
		t.Fatal(err)
	}
	response, err := c.Get(t.Context(), "GET", "报告 #.txt", "")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if string(data) != "abc" || grants != 2 || reads != 2 {
		t.Fatal(string(data), grants, reads)
	}
	if _, err = c.GetURL(t.Context(), "../git/a"); err == nil {
		t.Fatal("traversal accepted")
	}
}
