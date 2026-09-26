package appbootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "assets"), 0700)
	os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte(`export function mount(root) { root.textContent="Ledger" }`), 0600)
	os.WriteFile(filepath.Join(dir, "assets", "app.css"), []byte(`body {color: #123}`), 0600)
	os.WriteFile(filepath.Join(dir, "tiana.app.json"), []byte(`{"schema_version":1,"app_id":"billing","name":"账单","rendering":"csr","routing":"hash","entry":"assets/app.js","styles":["assets/app.css"],"database_instance_id":"ins_billing"}`), 0600)
	return dir
}

func TestLoadBuildAcceptsCSRModulesWithoutHTML(t *testing.T) {
	build, err := LoadBuild(buildFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	defer build.Close()
	if build.Manifest.Entry != "assets/app.js" || build.Manifest.AppID != "billing" {
		t.Fatal("manifest not loaded")
	}
}

func TestLoadBuildRejectsUnsupportedOrEscapingBuilds(t *testing.T) {
	for _, scenario := range []string{"ssr", "history", "html", "unknown", "outside", "symlink", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			dir := buildFixture(t)
			manifest := filepath.Join(dir, "tiana.app.json")
			bytes, _ := os.ReadFile(manifest)
			data := string(bytes)
			switch scenario {
			case "ssr":
				data = strings.Replace(data, `"csr"`, `"ssr"`, 1)
			case "history":
				data = strings.Replace(data, `"hash"`, `"history"`, 1)
			case "html":
				os.WriteFile(filepath.Join(dir, "index.html"), []byte("custom html"), 0600)
			case "unknown":
				data = strings.Replace(data, `"schema_version":1`, `"schema_version":1,"token":"forbidden"`, 1)
			case "outside":
				data = strings.Replace(data, "assets/app.js", "../app.js", 1)
			case "symlink":
				os.Symlink(filepath.Join(t.TempDir(), "secret"), filepath.Join(dir, "secret.js"))
			case "missing":
				os.Remove(filepath.Join(dir, "assets", "app.js"))
			}
			os.WriteFile(manifest, []byte(data), 0600)
			build, err := LoadBuild(dir)
			if err == nil {
				build.Close()
				t.Fatal("unsafe or unsupported build accepted")
			}
		})
	}
}

func TestStylelessApplicationUsesAnEmptyStylesList(t *testing.T) {
	dir := buildFixture(t)
	name := filepath.Join(dir, "tiana.app.json")
	bytes, _ := os.ReadFile(name)
	os.WriteFile(name, []byte(strings.Replace(string(bytes), `"styles":["assets/app.css"],`, "", 1)), 0600)
	build, err := LoadBuild(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer build.Close()
	if build.Manifest.Styles == nil {
		t.Fatal("Bootstrap requires an array for styleless apps")
	}
}
