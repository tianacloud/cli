package apppublish

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

func TestAppsCreateReusesCloudAuthentication(t *testing.T) {
	called := false
	r, _, out, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) {
		called = true
		if req.Method != "POST" || req.URL.Path != "/api/v1/apps" || req.Header.Get("Authorization") != "Bearer private-access" {
			t.Errorf("unexpected App request")
		}
		io.WriteString(w, `{"app_id":"AAAAAAAAAAAA","name":"Billing","owner_id":"prn-test","tenant_id":"ten-test"}`)
	})
	if code := runTest(r, out, context.Background(), []string{"web", "create", "Billing", "--json"}); code != 0 || !called {
		t.Fatalf("App create failed: %d %s", code, out)
	}
}

func TestGenericMGRFailurePreservesHTTPStatus(t *testing.T) {
	r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":"request failed"}`)
	})

	result := r.Run(t.Context(), Options{Command: "create", Name: "Billing", RequestID: "request-test"})
	if result.Error == nil || !strings.Contains(result.Error.Message, "HTTP 503") {
		t.Fatalf("error=%+v", result.Error)
	}
}

func TestUploadPreservesLayoutAndRefreshesExpiredAndUncertainPuts(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "任意目录"), 0700)
	contents := []byte("console.log('hello')")
	os.WriteFile(filepath.Join(dir, "任意目录", "页面 #1.js"), contents, 0600)
	os.WriteFile(filepath.Join(dir, "entry.htm"), []byte("hello"), 0600)
	var puts atomic.Int32
	stored := map[string]bool{}
	var manifest artifactManifest
	var fingerprint, version string
	var plans int
	objects := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
			t.Error("management credentials reached object storage")
		}
		data, _ := io.ReadAll(req.Body)
		sum := md5.Sum(data)
		if req.Method != "PUT" || base64.StdEncoding.EncodeToString(sum[:]) != req.Header.Get("Content-MD5") {
			t.Error("PUT integrity headers did not match body")
		}
		stored[strings.TrimPrefix(req.URL.Path, "/objects/")] = true
		if puts.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	}))
	defer objects.Close()
	r, _, out, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method == "PUT" {
			json.NewDecoder(req.Body).Decode(&manifest)
			raw, _ := json.Marshal(manifest)
			sum := sha256.Sum256(raw)
			fingerprint = hex.EncodeToString(sum[:])
			version = pathLast(req.URL.Path)
			json.NewEncoder(w).Encode(artifactVersion{TenantID: "ten-test", AppID: "billing", VersionID: version, Fingerprint: fingerprint, State: "uploading"})
			return
		}
		if strings.HasSuffix(req.URL.Path, "/uploads") {
			plans++
			var files []artifactLink
			for _, f := range manifest.Files {
				link := artifactLink{Path: f.Path, Uploaded: stored[f.Path], Method: "PUT", URL: objects.URL + "/objects/" + escapeArtifactPath(f.Path), ExpiresAt: time.Now().Add(time.Minute), Headers: map[string]string{"Content-Type": f.ContentType, "Content-MD5": f.MD5, "X-Oss-Forbid-Overwrite": "true"}}
				if plans == 1 {
					link.ExpiresAt = time.Now().Add(-time.Second)
				}
				files = append(files, link)
			}
			json.NewEncoder(w).Encode(map[string]any{"files": files})
			return
		}
		if strings.HasSuffix(req.URL.Path, "/complete") {
			if len(stored) != 2 {
				t.Error("published before upload complete")
			}
			json.NewEncoder(w).Encode(artifactVersion{TenantID: "ten-test", AppID: "billing", VersionID: version, Fingerprint: fingerprint, State: "published"})
			return
		}
		t.Errorf("unexpected request: %s", req.URL.Path)
	})
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: objects.Certificate().Raw}), 0600)
	if code := runTest(r, out, context.Background(), []string{"web", "upload", "billing", "--dir", dir, "--entry", "entry.htm", "--upload-ca-file", caFile, "--json"}); code != 0 {
		t.Fatalf("upload failed: %s", out)
	}
	if plans != 3 || puts.Load() != 2 || !stored["任意目录/页面 #1.js"] || manifest.EntryPath != "entry.htm" {
		t.Fatalf("layout/recovery: plans=%d puts=%d", plans, puts.Load())
	}
	if strings.Contains(out.String(), objects.URL) || strings.Contains(out.String(), "private-access") {
		t.Fatal("upload output exposed credentials or signed links")
	}
	if !strings.HasPrefix(version, "v-") || len(version) != 66 {
		t.Fatal("default version is not content derived")
	}
}

func TestEmptyArtifactHasZeroContentLength(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "empty"), nil, 0600)
	root, _ := os.OpenRoot(dir)
	defer root.Close()
	m, _ := scanArtifacts(t.Context(), root, "")
	objects := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			t.Errorf("empty signed PUT was sent with length %d and encoding %v", r.ContentLength, r.TransferEncoding)
		}
	}))
	defer objects.Close()
	f := m.Files[0]
	r := Runner{UploadHTTP: objects.Client()}
	err := r.putArtifact(t.Context(), root, f, artifactLink{Method: "PUT", URL: objects.URL, Headers: map[string]string{"Content-MD5": f.MD5, "Content-Type": f.ContentType, "Content-Length": "0", "X-Oss-Forbid-Overwrite": "true"}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUploadClientBypassesProcessProxy(t *testing.T) {
	client := newUploadHTTPClient(nil)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("upload transport type = %T", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("signed object uploads must not use the process proxy")
	}
}

func pathLast(p string) string { parts := strings.Split(p, "/"); return parts[len(parts)-1] }
func escapeArtifactPath(p string) string {
	parts := strings.Split(p, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

func TestBuildDirectoryRejectsLinksAndMissingEntry(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "file.dat"), []byte("data"), 0600)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := scanArtifacts(t.Context(), root, "absent.html"); err == nil {
		t.Fatal("accepted missing entry")
	}
	if err := os.Symlink(filepath.Join(dir, "file.dat"), filepath.Join(dir, "link")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := scanArtifacts(t.Context(), root, ""); err == nil {
		t.Fatal("accepted symbolic link")
	}
}

func TestPutDoesNotFollowRedirectOrSendChangedFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x"), []byte("old"), 0600)
	root, _ := os.OpenRoot(dir)
	defer root.Close()
	m, _ := scanArtifacts(t.Context(), root, "")
	var called atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called.Add(1) }))
	defer target.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer source.Close()
	f := m.Files[0]
	link := artifactLink{Method: "PUT", URL: source.URL, Headers: map[string]string{"Content-MD5": f.MD5, "Content-Type": f.ContentType, "X-Oss-Forbid-Overwrite": "true"}}
	r := Runner{UploadHTTP: source.Client()}
	if err := r.putArtifact(t.Context(), root, f, link); err == nil || called.Load() != 0 {
		t.Fatal("followed object upload redirect")
	}
	os.WriteFile(filepath.Join(dir, "x"), []byte("new"), 0600)
	if err := r.putArtifact(t.Context(), root, f, link); err == nil || err.Code != "ARTIFACT_CHANGED" {
		t.Fatal("uploaded changed artifact")
	}
}

func runnerForTest(t *testing.T, handler http.HandlerFunc) (Runner, string, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	store := authclient.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"), server.URL)
	if err := store.Save(authclient.Credential{AccessToken: "private-access", RefreshToken: "private-refresh", ExpiresAt: time.Now().Add(time.Hour), User: authclient.User{ID: "prn-test"}}); err != nil {
		t.Fatal(err)
	}
	client, err := authclient.NewWithConfig(authclient.Config{Origin: server.URL, Store: store, HTTPClient: server.Client(), NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	return Runner{Client: client}, "prn-test", &bytes.Buffer{}, &bytes.Buffer{}
}
func runTest(r Runner, out *bytes.Buffer, ctx context.Context, args []string) int {
	f := flag.NewFlagSet("test", flag.ContinueOnError)
	o := Options{Command: args[1], RequestID: "test-request"}
	if len(args) > 2 {
		if o.Command == "create" {
			o.Name = args[2]
		} else {
			o.AppID = args[2]
		}
	}
	f.StringVar(&o.Dir, "dir", "", "")
	f.StringVar(&o.Version, "version", "", "")
	f.StringVar(&o.Entry, "entry", "", "")
	f.StringVar(&o.UploadCAFile, "upload-ca-file", "", "")
	f.Bool("json", false, "")
	if err := f.Parse(args[3:]); err != nil {
		return 2
	}
	result := r.Run(ctx, o)
	json.NewEncoder(out).Encode(result)
	if result.Error != nil {
		return result.Error.ExitCode
	}
	return 0
}
func TestUploadCancellationStopsNetworkRequest(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x"), []byte("data"), 0600)
	root, _ := os.OpenRoot(dir)
	defer root.Close()
	m, _ := scanArtifacts(t.Context(), root, "")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	called := false
	objects := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer objects.Close()
	f := m.Files[0]
	r := Runner{UploadHTTP: objects.Client()}
	err := r.putArtifact(ctx, root, f, artifactLink{Method: "PUT", URL: objects.URL, Headers: map[string]string{"Content-MD5": f.MD5, "Content-Type": f.ContentType, "X-Oss-Forbid-Overwrite": "true"}})
	if err == nil || called {
		t.Fatal("canceled upload reached object store")
	}
}
