package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/testutil/proxypeer"
)

func TestLoginStatusAndTemplateDownloadThroughAllProxy(t *testing.T) {
	for _, kind := range []string{"http", "socks5h"} {
		t.Run(kind, func(t *testing.T) {
			const origin = "https://mgr.example.test"
			const endpoint = "ep-00000000000000000000000000.web.example.test"
			var zipped bytes.Buffer
			z := zip.NewWriter(&zipped)
			for name, body := range map[string]string{"metadata.json": `{"name":"notes"}`, "README.md": "source"} {
				w, err := z.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = io.WriteString(w, body)
			}
			if err := z.Close(); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(zipped.Bytes())
			var exchanges, refreshes, pages, downloads, publications, statuses, fileReads, fileWrites, shareWrites, shareCompletes atomic.Int32
			var forceRefresh, losePublication atomic.Bool
			var accessToken atomic.Value
			accessToken.Store("synthetic-access")
			var receivedPublishID, expectedRecoveryID atomic.Value
			receivedPublishID.Store("")
			expectedRecoveryID.Store("")
			server, _ := proxypeer.TLS(t, "mgr.example.test,"+endpoint, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Proxy-Authorization") != "" {
					t.Error("proxy credential reached target")
				}
				switch r.URL.Path {
				case "/api/v1/instances":
					pages.Add(1)
					if r.URL.Query().Get("page") == "2" {
						io.WriteString(w, `{"items":[{"id":"second","display_name":"second","engine":"sqlite"}],"total_pages":2}`)
					} else {
						io.WriteString(w, `{"items":[{"id":"first","display_name":"first","engine":"sqlite"}],"total_pages":2}`)
					}
				case "/api/v1/files/read-grant", "/api/v1/web-projects/a1b2c3d4e5f6/file-storage/read-grant":
					json.NewEncoder(w).Encode(map[string]any{"token": "file-read-token", "origin": origin, "root": "/files/", "scope_id": "scope", "expires_at": time.Now().Add(time.Minute).Unix()})
				case "/api/v1/files/uploads":
					json.NewEncoder(w).Encode(map[string]any{"id": "upload-one", "path": "hello.txt", "status": "pending", "form": map[string]any{"url": origin + "/object-upload", "fields": map[string]string{"key": "synthetic-object", "policy": "signed"}, "expires_at": time.Now().Add(time.Minute).Unix()}})
				case "/api/v1/files/uploads/upload-one/complete":
					io.WriteString(w, `{"id":"upload-one","path":"hello.txt","completed":true,"status":"completed"}`)
				case "/files/hello.txt":
					fileReads.Add(1)
					if r.Header.Get("Authorization") != "Bearer file-read-token" {
						t.Error("wrong file read credential")
					}
					io.WriteString(w, "hello")
				case "/object-upload", "/share-upload":
					expected := []byte("hello")
					if r.URL.Path == "/share-upload" {
						shareWrites.Add(1)
						expected = zipped.Bytes()
					} else {
						fileWrites.Add(1)
					}
					if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
						t.Error("account credential reached file upload")
					}
					reader, err := r.MultipartReader()
					if err != nil {
						t.Error(err)
						return
					}
					found := false
					for {
						part, err := reader.NextPart()
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Error(err)
							return
						}
						data, _ := io.ReadAll(part)
						if part.FormName() == "file" {
							found = true
							if !bytes.Equal(data, expected) {
								t.Error("wrong uploaded bytes")
							}
						}
					}
					if !found {
						t.Error("missing upload file part")
					}
					w.WriteHeader(201)
				case "/api/v1/web-projects/a1b2c3d4e5f6":
					io.WriteString(w, `{"id":"a1b2c3d4e5f6","name":"Proxy fixture","project_revision":1,"instance_id":"web-instance","owner_id":"owner","tenant_id":"tenant","entry":"app.js","endpoint":"https://mgr.example.test/web/a1b2c3d4e5f6/"}`)
				case "/api/v1/instances/web-instance":
					json.NewEncoder(w).Encode(map[string]any{"id": "web-instance", "engine": "web", "product_state": "ACTIVE", "connection": map[string]string{"hostname": endpoint, "url": "https://" + endpoint}})
				case "/v1/fetch":
					if r.Header.Get("Authorization") != "Bearer "+accessToken.Load().(string) {
						t.Error("Web data credential missing")
					}
					var frame [10]byte
					if _, err := io.ReadFull(r.Body, frame[:]); err != nil {
						t.Error(err)
						return
					}
					raw := make([]byte, binary.BigEndian.Uint32(frame[6:]))
					if _, err := io.ReadFull(r.Body, raw); err != nil {
						t.Error(err)
						return
					}
					var request struct {
						RequestID string                         `json:"request_id"`
						Method    string                         `json:"method"`
						Path      string                         `json:"path_query"`
						Headers   []struct{ Name, Value string } `json:"headers"`
					}
					if err := json.Unmarshal(raw, &request); err != nil {
						t.Error(err)
						return
					}
					body := map[string]any{"serving_sha256": "current"}
					if request.Method == "PUT" {
						publications.Add(1)
						archive, _ := io.ReadAll(r.Body)
						if !bytes.HasPrefix(archive, []byte("PK")) {
							t.Error("Web archive missing")
						}
						body = map[string]any{"uploaded": true}
						for _, h := range request.Headers {
							if h.Name == "x-tiana-publish-id" {
								body["publish_id"] = h.Value
								receivedPublishID.Store(h.Value)
							}
							if h.Name == "x-tiana-content-sha256" {
								body["sha256"] = h.Value
							}
						}
					} else {
						statuses.Add(1)
						if id := expectedRecoveryID.Load().(string); id != "" && request.Path != "/_tiana/web/publish/"+id {
							t.Error("receipt recovery queried a different publication identity")
						}
					}
					payload, _ := json.Marshal(body)
					declaredLength := len(payload)
					if request.Method == "PUT" && losePublication.CompareAndSwap(true, false) {
						// The archive arrived, but the success response was lost.
						// A valid outer frame with truncated body is an unknown
						// outcome, and must retain identity without another PUT.
						payload = nil
					}
					meta, _ := json.Marshal(struct {
						Version int    `json:"metadata_version"`
						ID      string `json:"request_id"`
						Status  int    `json:"status"`
						Text    string `json:"status_text"`
						Headers []any  `json:"headers"`
						Length  int    `json:"body_length"`
					}{1, request.RequestID, 200, "OK", []any{}, declaredLength})
					copy(frame[:4], "TFS1")
					binary.BigEndian.PutUint32(frame[6:], uint32(len(meta)))
					w.Header().Set("Content-Type", "application/vnd.tiana.fetch.v1")
					w.Header().Set("Cache-Control", "no-store")
					w.Write(frame[:])
					w.Write(meta)
					w.Write(payload)
				case "/api/v1/auth/transactions":
					json.NewEncoder(w).Encode(map[string]any{"transaction_id": "proxy-login", "client_secret": "synthetic-secret", "user_code": "TEST", "verification_uri_complete": origin + "/approve", "expires_in": 600, "poll_interval": 1})
				case "/api/v1/auth/transactions/proxy-login/poll":
					io.WriteString(w, `{"status":"approved","authorization_code":"synthetic-code"}`)
				case "/api/v1/auth/token":
					exchanges.Add(1)
					io.WriteString(w, `{"access_token":"synthetic-access","refresh_token":"synthetic-refresh","expires_in":3600,"user":{"user_id":"owner","tenant_id":"tenant"}}`)
				case "/api/v1/auth/refresh":
					refreshes.Add(1)
					accessToken.Store("synthetic-access-rotated")
					io.WriteString(w, `{"access_token":"synthetic-access-rotated","refresh_token":"synthetic-refresh-rotated","expires_in":3600}`)
				case "/api/v1/auth/transactions/whoami":
					if forceRefresh.CompareAndSwap(true, false) {
						w.WriteHeader(401)
						io.WriteString(w, `{"error":"expired"}`)
						return
					}
					if r.Header.Get("Authorization") != "Bearer "+accessToken.Load().(string) {
						t.Error("account credential missing")
					}
					io.WriteString(w, `{"user":{"user_id":"owner","tenant_id":"tenant","email":"owner@example.test"}}`)
				case "/api/v1/usage":
					io.WriteString(w, testQuotaResponse)
				case "/api/v1/web-templates":
					io.WriteString(w, `{"items":[{"name":"notes","description":"proxy fixture"}],"next_cursor":""}`)
				case "/api/v1/web-templates/notes/init":
					json.NewEncoder(w).Encode(map[string]any{"name": "notes", "zip_size": zipped.Len(), "zip_sha256": hex.EncodeToString(digest[:]), "download_url": origin + "/private.zip?signature=synthetic-signed-url"})
				case "/api/v1/shared-templates/a1b2c3d4e5f6/source":
					json.NewEncoder(w).Encode(map[string]any{"name": "notes", "size": zipped.Len(), "sha256": hex.EncodeToString(digest[:]), "url": origin + "/private.zip?signature=synthetic-signed-url"})
				case "/api/v1/web-projects/a1b2c3d4e5f6/share-template/uploads":
					json.NewEncoder(w).Encode(map[string]any{"job_id": 7, "status": "uploading", "form": map[string]any{"url": origin + "/share-upload", "fields": map[string]string{"key": "synthetic-share-object"}}})
				case "/api/v1/web-projects/a1b2c3d4e5f6/share-template/uploads/7/complete":
					shareCompletes.Add(1)
					if shareWrites.Load() != 1 {
						t.Error("share completed before upload")
					}
					io.WriteString(w, `{"job_id":7,"status":"pending"}`)
				case "/private.zip":
					downloads.Add(1)
					if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
						t.Error("account credential reached object read")
					}
					w.Write(zipped.Bytes())
				default:
					t.Errorf("unexpected proxy fixture request: %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			var peer *proxypeer.Proxy
			if kind == "http" {
				peer = proxypeer.HTTP(t, server.Listener.Addr().String(), false)
			} else {
				peer = proxypeer.SOCKS(t, server.Listener.Addr().String(), false)
			}
			newTestEnv(t, origin)
			for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
				t.Setenv(name, "")
			}
			t.Setenv("ALL_PROXY", peer.URL)
			ca := filepath.Join(t.TempDir(), "ca.pem")
			if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "notes")
			sharedTarget := filepath.Join(t.TempDir(), "shared-notes")
			installZIP := filepath.Join(t.TempDir(), "install.zip")
			if err := os.WriteFile(installZIP, zipped.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			download := filepath.Join(t.TempDir(), "download.txt")
			source := filepath.Join(t.TempDir(), "source.txt")
			if err := os.WriteFile(source, []byte("hello"), 0600); err != nil {
				t.Fatal(err)
			}
			build := t.TempDir()
			if err := os.WriteFile(filepath.Join(build, "app.js"), []byte("export function mount() {}"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"login", "--start", "--no-open", "--json"}, {"login", "--resume", "--json"}, {"status", "--json"}, {"sqlite", "list", "--json"}, {"web", "list-template", "--json"}, {"web", "init-template", "notes", "--dir", target, "--json"}, {"web", "publish", "a1b2c3d4e5f6", "--dir", build, "--json"}, {"web", "status", "a1b2c3d4e5f6", "--json"}, {"web", "status", "a1b2c3d4e5f6", "--publish-id", "receipt", "--json"}, {"file", "get", "hello.txt", download, "--json"}, {"file", "put", source, "hello.txt", "--json"}, {"web", "init-share-template", origin + "/shared-templates/a1b2c3d4e5f6", "--dir", sharedTarget, "--json"}, {"web", "publish-share-template", "a1b2c3d4e5f6", "--file", installZIP, "--json"}} {
				if args[0] == "status" {
					forceRefresh.Store(true)
				}
				var output, diagnostics bytes.Buffer
				code := runCLI(context.Background(), append([]string{"--ca-file", ca}, args...), strings.NewReader(""), &output, &diagnostics)
				if code != 0 && !(args[0] == "login" && args[1] == "--start" && code == 3) {
					t.Fatalf("%v code=%d output=%s diag=%s", args, code, &output, &diagnostics)
				}
				if !json.Valid(output.Bytes()) || strings.Contains(output.String()+diagnostics.String(), "synthetic-signed-url") {
					t.Fatal("invalid JSON or signed URL leak")
				}
				for _, canary := range []string{"synthetic-access", "synthetic-refresh", "synthetic-code", "synthetic-secret"} {
					if strings.Contains(output.String()+diagnostics.String(), canary) {
						t.Fatal("credential canary reached CLI output")
					}
				}
				if args[0] == "sqlite" && (!strings.Contains(output.String(), "first") || !strings.Contains(output.String(), "second")) {
					t.Fatal("management JSON missed a proxy-fetched page")
				}
			}
			previewFileThroughCLI(t, ca, build)
			losePublication.Store(true)
			var unknownOutput, unknownDiagnostics bytes.Buffer
			code := runCLI(t.Context(), []string{"--ca-file", ca, "web", "publish", "a1b2c3d4e5f6", "--dir", build, "--json"}, strings.NewReader(""), &unknownOutput, &unknownDiagnostics)
			var unknown struct {
				Status string `json:"status"`
				Data   struct {
					PublishID string `json:"publish_id"`
					Archive   string `json:"archive_path"`
				} `json:"data"`
			}
			if code != 4 || json.Unmarshal(unknownOutput.Bytes(), &unknown) != nil || unknown.Status != "unknown" || unknown.Data.PublishID == "" || unknown.Data.Archive == "" {
				t.Fatalf("unknown publication lost identity: code=%d output=%s diagnostics=%s", code, &unknownOutput, &unknownDiagnostics)
			}
			t.Cleanup(func() { os.Remove(unknown.Data.Archive) })
			if info, err := os.Stat(unknown.Data.Archive); err != nil || !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
				t.Fatal("unknown publication lost private archive", err)
			}
			if receivedPublishID.Load().(string) != unknown.Data.PublishID {
				t.Fatal("unknown publication replaced the identity sent to the App")
			}
			expectedRecoveryID.Store(unknown.Data.PublishID)
			var receiptOutput, receiptDiagnostics bytes.Buffer
			code = runCLI(t.Context(), []string{"--ca-file", ca, "web", "status", "a1b2c3d4e5f6", "--publish-id", unknown.Data.PublishID, "--json"}, strings.NewReader(""), &receiptOutput, &receiptDiagnostics)
			if code != 0 || !strings.Contains(receiptOutput.String(), `"query_kind":"receipt"`) {
				t.Fatalf("receipt recovery: %d %s %s", code, &receiptOutput, &receiptDiagnostics)
			}
			if exchanges.Load() != 1 || refreshes.Load() != 1 || pages.Load() != 2 || downloads.Load() != 2 || publications.Load() != 2 || statuses.Load() != 3 || fileReads.Load() != 2 || fileWrites.Load() != 1 || shareWrites.Load() != 1 || shareCompletes.Load() != 1 {
				t.Fatal("exchange replay or missing object download")
			}
			if _, err := os.Stat(filepath.Join(target, "README.md")); err != nil {
				t.Fatal(err)
			}
			for _, r := range peer.Requests() {
				if (r.Address != "mgr.example.test:443" && r.Address != endpoint+":443") || r.Authorization != "" {
					t.Error("proxy route or credential leak")
				}
			}
		})
	}
}
