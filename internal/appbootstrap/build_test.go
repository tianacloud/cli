package appbootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

func buildFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "assets"), 0700)
	os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte(`export function mount(root){root.textContent="Ledger"}`), 0600)
	os.WriteFile(filepath.Join(dir, "assets", "app.css"), []byte(`body {color:#123}`), 0600)
	return dir
}
func fixtureDescriptor() Manifest {
	return Manifest{WebID: "billing", Name: "账单", ProjectRevision: 1, Entry: "assets/app.js", DatabaseInstanceID: "ins_billing"}
}
func TestLoadBuildUsesProjectParametersWithoutManifest(t *testing.T) {
	b, e := LoadBuild(buildFixture(t), fixtureDescriptor())
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	if b.Manifest.Entry != "assets/app.js" || b.Manifest.Rendering != "csr" || b.Manifest.Routing != "hash" {
		t.Fatal("fixed project descriptor lost")
	}
}
func TestLoadBuildRejectsUnsafeAssetsAndLegacyMetadata(t *testing.T) {
	for _, scenario := range []string{"html", "legacy", "outside", "symlink", "missing", "hidden", "missing-project", "entry-null"} {
		t.Run(scenario, func(t *testing.T) {
			dir := buildFixture(t)
			m := fixtureDescriptor()
			switch scenario {
			case "html":
				os.WriteFile(filepath.Join(dir, "index.html"), []byte("custom HTML"), 0600)
			case "legacy":
				os.WriteFile(filepath.Join(dir, "tiana.app.json"), []byte(`{"entry":"other.js"}`), 0600)
			case "outside":
				m.Entry = "../app.js"
			case "symlink":
				os.Symlink(filepath.Join(t.TempDir(), "secret"), filepath.Join(dir, "secret.js"))
			case "missing":
				os.Remove(filepath.Join(dir, "assets", "app.js"))
			case "hidden":
				m.Entry = ".private/app.js"
			case "missing-project":
				m.ProjectRevision = 0
			case "entry-null":
				m.Entry = ""
			}
			b, e := LoadBuild(dir, m)
			if e == nil {
				b.Close()
				t.Fatal("unsafe or stale build accepted")
			}
		})
	}
}
func TestLoadBuildDatabaseAndGitAreOptional(t *testing.T) {
	for _, git := range []string{"", "git-source"} {
		m := fixtureDescriptor()
		m.DatabaseInstanceID = ""
		m.GitInstanceID = git
		b, e := LoadBuild(buildFixture(t), m)
		if e != nil {
			t.Fatal(e)
		}
		b.Close()
	}
}
func TestCSSIsOrdinaryAssetNotProjectConfiguration(t *testing.T) {
	dir := buildFixture(t)
	os.Remove(filepath.Join(dir, "assets", "app.css"))
	b, e := LoadBuild(dir, fixtureDescriptor())
	if e != nil {
		t.Fatal(e)
	}
	b.Close()
}
