package apppublish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadKeepsFileInventoryLocallyAndCompletesOnce(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 65; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%02d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var prepared artifactVersion
	completes := 0
	r, _, out, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method == "PUT" {
			var m artifactManifest
			json.NewDecoder(req.Body).Decode(&m)
			raw, _ := json.Marshal(m)
			sum := sha256.Sum256(raw)
			prepared = artifactVersion{TenantID: "ten-test", ID: "billing", VersionID: pathLast(req.URL.Path), Fingerprint: hex.EncodeToString(sum[:]), State: "uploading"}
			json.NewEncoder(w).Encode(prepared)
			return
		}
		if strings.HasSuffix(req.URL.Path, "/uploads") {
			var body struct {
				Files []artifactFile `json:"files"`
			}
			json.NewDecoder(req.Body).Decode(&body)
			var links []artifactLink
			for _, f := range body.Files {
				p := f.Path
				links = append(links, artifactLink{Path: p, Uploaded: true})
			}
			json.NewEncoder(w).Encode(map[string]any{"files": links})
			return
		}
		if strings.HasSuffix(req.URL.Path, "/complete") {
			completes++
			prepared.State = "published"
			json.NewEncoder(w).Encode(prepared)
			return
		}
		t.Errorf("unexpected request %s", req.URL.Path)
	})
	if code := runTest(r, out, context.Background(), []string{"web", "upload", "billing", "--dir", dir, "--json"}); code != 0 || completes != 1 {
		t.Fatalf("single complete code=%d requests=%d result=%s", code, completes, out)
	}
}

func TestCompleteRejectsNoProgressAndChangedTenant(t *testing.T) {
	for _, variant := range []string{"no-progress", "tenant"} {
		t.Run(variant, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "file"), nil, 0600)
			var prepared artifactVersion
			requests := 0
			r, _, out, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) {
				if req.Method == "PUT" {
					var m artifactManifest
					json.NewDecoder(req.Body).Decode(&m)
					raw, _ := json.Marshal(m)
					sum := sha256.Sum256(raw)
					prepared = artifactVersion{TenantID: "tenant-a", ID: "billing", VersionID: pathLast(req.URL.Path), Fingerprint: hex.EncodeToString(sum[:]), State: "uploading"}
					json.NewEncoder(w).Encode(prepared)
					return
				}
				if strings.HasSuffix(req.URL.Path, "/uploads") {
					json.NewEncoder(w).Encode(map[string]any{"files": []artifactLink{{Path: "file", Uploaded: true}}})
					return
				}
				requests++
				if variant == "tenant" {
					prepared.TenantID = "tenant-b"
					prepared.State = "published"

				}
				json.NewEncoder(w).Encode(prepared)
			})
			if code := runTest(r, out, t.Context(), []string{"web", "upload", "billing", "--dir", dir, "--json"}); code == 0 || requests != 1 {
				t.Fatalf("accepted invalid complete code=%d requests=%d result=%s", code, requests, out)
			}
		})
	}
}

func TestPublishedPrepareDoesNotRequireStoredFileCount(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "file"), nil, 0600)
	r, _, out, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "PUT" {
			t.Fatal("published prepare must not request another upload")
		}
		var m artifactManifest
		json.NewDecoder(req.Body).Decode(&m)
		raw, _ := json.Marshal(m)
		sum := sha256.Sum256(raw)
		json.NewEncoder(w).Encode(artifactVersion{TenantID: "tenant-a", ID: "billing", VersionID: pathLast(req.URL.Path), Fingerprint: hex.EncodeToString(sum[:]), State: "published"})
	})
	if code := runTest(r, out, t.Context(), []string{"web", "upload", "billing", "--dir", dir, "--json"}); code != 0 {
		t.Fatalf("published version should not require a stored file count: %s", out)
	}
}
