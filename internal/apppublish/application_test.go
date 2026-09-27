package apppublish

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func csrBuild(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "assets"), 0700)
	os.WriteFile(filepath.Join(dir, "assets/app.js"), []byte("export function mount(){}"), 0600)
	os.WriteFile(filepath.Join(dir, "tiana.app.json"), []byte(`{"schema_version":1,"app_id":"billing","name":"Billing","rendering":"csr","routing":"hash","entry":"assets/app.js","styles":[],"database_instance_id":"sqlite-billing"}`), 0600)
	return dir
}
func TestCSRScanKeepsManifestInManagementPlane(t *testing.T) {
	dir := csrBuild(t)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	m, e := scanArtifacts(t.Context(), root, "")
	if e != nil {
		t.Fatal(e)
	}
	if len(m.Files) != 1 || m.Files[0].Path != "assets/app.js" {
		t.Fatalf("private app manifest entered object upload: %+v", m.Files)
	}
	raw, _ := json.Marshal(m)
	var v map[string]any
	json.Unmarshal(raw, &v)
	if v["application"] == nil {
		t.Fatal("runtime binding missing from management manifest")
	}
}
func TestCSRScanRejectsHTMLBeforeUpload(t *testing.T) {
	dir := csrBuild(t)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("custom document"), 0600)
	root, _ := os.OpenRoot(dir)
	defer root.Close()
	if _, e := scanArtifacts(t.Context(), root, ""); e == nil {
		t.Fatal("uploaded custom HTML alongside fixed Bootstrap application")
	}
}

func TestCSRUploadReturnsOnlyConfirmedApplicationLinks(t *testing.T) {
	for _, invalid := range []string{"", "database", "repository", "commit"} {
		t.Run("receipt-"+invalid, func(t *testing.T) {
			dir := csrBuild(t)
			name := filepath.Join(dir, "tiana.app.json")
			raw, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			raw = []byte(strings.Replace(string(raw), `"schema_version":1`, `"schema_version":1,"git_instance_id":"git-source","source_commit":"`+strings.Repeat("a", 40)+`"`, 1))
			if err = os.WriteFile(name, raw, 0600); err != nil {
				t.Fatal(err)
			}
			var manifest artifactManifest
			requested := false
			descriptorRequestID := ""
			r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case "PUT":
					json.NewDecoder(r.Body).Decode(&manifest)
					raw, _ := json.Marshal(manifest)
					hash := sha256.Sum256(raw)
					json.NewEncoder(w).Encode(artifactVersion{TenantID: "tenant", ID: "billing", VersionID: "v1", State: "published", Fingerprint: hex.EncodeToString(hash[:])})
				case "GET":
					requested = true
					descriptorRequestID = r.Header.Get("X-Request-ID")
					if r.URL.Path != "/api/v1/web-projects/billing/versions/v1/bootstrap" {
						t.Errorf("wrong route %s", r.URL.Path)
					}
					app := *manifest.Application
					switch invalid {
					case "database":
						app.DatabaseInstanceID = "sqlite-other"
					case "repository":
						app.GitInstanceID = "git-other"
					case "commit":
						app.SourceCommit = strings.Repeat("b", 40)
					}
					data, _ := json.Marshal(app)
					var view map[string]any
					json.Unmarshal(data, &view)
					view["version_id"] = "v1"
					view["asset_base"] = "https://cdn.example/tenant/billing/v1/"
					view["application_url"] = "https://console.example/web/billing/"
					view["version_url"] = "https://console.example/web/billing/?version=v1"
					json.NewEncoder(w).Encode(view)
				default:
					t.Errorf("unexpected %s", r.Method)
				}
			})
			result := r.Run(t.Context(), Options{Command: "upload", ID: "billing", Dir: dir, Version: "v1"})
			if !requested {
				t.Fatal("did not confirm published Bootstrap metadata")
			}
			if invalid != "" {
				if result.Error == nil {
					t.Fatal("reported wrong binding as published app")
				}
				if descriptorRequestID == "" || result.Error.RequestID != descriptorRequestID {
					t.Fatal("hosting confirmation failure lost its request ID")
				}
				return
			}
			if result.Error != nil {
				t.Fatal(result.Error)
			}
			data := result.Data.(map[string]any)
			if data["application_url"] != "https://console.example/web/billing/" || data["version_url"] != "https://console.example/web/billing/?version=v1" {
				t.Fatal("missing confirmed app links")
			}
		})
	}
}
