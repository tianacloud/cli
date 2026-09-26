package apppublish

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// MGR owns OSS signing and object access policy. The CLI must pass through its
// scoped upload plan, including headers added by a new shared-bucket policy.
func TestUploadPlanPreservesPrivateACLPublicTagAndSignedURL(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	contents := "export function mount(){}"
	if err := os.WriteFile(filepath.Join(dir, "assets", "app v1.js"), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	manifest, scanErr := scanArtifacts(t.Context(), root, "")
	if scanErr != nil {
		t.Fatal(scanErr)
	}
	file := manifest.Files[0]
	const target = "/ten-test/web/billing/v1/assets/app%20v1.js?OSSAccessKeyId=fixture&Expires=4102444800&Signature=fixture%2Bsignature%2F%3D"
	headers := map[string]string{
		"Content-Type":            file.ContentType,
		"Content-MD5":             file.MD5,
		"Content-Length":          strconv.FormatInt(file.Size, 10),
		"Cache-Control":           "public, max-age=31536000, immutable",
		"x-oss-forbid-overwrite":  "true",
		"x-oss-object-acl":        "private",
		"x-oss-tagging":           "tiana-public=true",
		"x-oss-meta-tiana-sha256": file.SHA256,
	}
	var puts atomic.Int32
	objects := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		puts.Add(1)
		if req.Method != http.MethodPut || req.RequestURI != target {
			t.Errorf("signed upload target changed: %s %s", req.Method, req.RequestURI)
		}
		for key, want := range headers {
			if got := req.Header.Get(key); got != want {
				t.Errorf("signed header %s = %q, want %q", key, got, want)
			}
		}
		if req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
			t.Error("management credentials reached the object upload")
		}
		body, readErr := io.ReadAll(req.Body)
		if readErr != nil || string(body) != contents || req.ContentLength != file.Size {
			t.Errorf("upload body changed: bytes=%d length=%d error=%v", len(body), req.ContentLength, readErr)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer objects.Close()
	base := "/api/v1/web-projects/billing/versions/v1"
	runner, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost || req.URL.Path != base+"/uploads" {
			t.Errorf("unexpected management request: %s %s", req.Method, req.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"files": []artifactLink{{
			Path: file.Path, Method: http.MethodPut, URL: objects.URL + target,
			Headers: headers, ExpiresAt: time.Now().Add(time.Minute),
		}}})
	})
	runner.UploadHTTP = objects.Client()
	if err := runner.uploadArtifactBatch(t.Context(), identity{PrincipalID: "prn-test"}, base, root, manifest.Files); err != nil {
		t.Fatal(err)
	}
	if puts.Load() != 1 {
		t.Fatalf("object PUT count = %d, want 1", puts.Load())
	}
}
