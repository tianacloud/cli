package webtemplate

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func zipFixture(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, data := range files {
		file, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestMaterializePreservesSourceMetadataAndRandomVariables(t *testing.T) {
	files := map[string]string{"metadata.json": `{"name":"notes","variables":[{"name":"WEB_ID","placeholder":"__TIANA_WEB_ID_ab1234__","files":["public/config.json"]}],"schema":"missing.sql"}`, "public/config.json": `{"web":"__TIANA_WEB_ID_ab1234__"}`, ".gitignore": "node_modules/\n", "src/子目录/main.js": "export const text = 'source';"}
	data := zipFixture(t, files)
	target := filepath.Join(t.TempDir(), "project with spaces")
	result, err := Materialize(t.Context(), Archive{Name: "notes", Reader: bytes.NewReader(data), Size: int64(len(data))}, target)
	if err != nil {
		t.Fatal(err)
	}
	if result.Directory != target || result.MetadataPath != filepath.Join(target, "metadata.json") {
		t.Fatalf("result: %+v", result)
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(name)))
		if err != nil || string(got) != want {
			t.Fatalf("%s: %q %v", name, got, err)
		}
	}
}

func TestMaterializeFailureLeavesNoProject(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
	}{
		{"wrong metadata name", map[string]string{"metadata.json": `{"name":"other"}`}},
		{"missing metadata", map[string]string{"app.js": "source"}},
		{"parent path", map[string]string{"metadata.json": `{"name":"notes"}`, "../outside": "bad"}},
		{"absolute path", map[string]string{"metadata.json": `{"name":"notes"}`, "/outside": "bad"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := zipFixture(t, tc.files)
			parent := t.TempDir()
			target := filepath.Join(parent, "project")
			if _, err := Materialize(t.Context(), Archive{Name: "notes", Reader: bytes.NewReader(data), Size: int64(len(data))}, target); err == nil {
				t.Fatal("bad ZIP accepted")
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("partial project: %v", err)
			}
			entries, _ := os.ReadDir(parent)
			if len(entries) != 0 {
				t.Fatal("temporary project was not cleaned")
			}
		})
	}
}

func TestMaterializeKeepsExistingProject(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "project")
	os.Mkdir(target, 0755)
	os.WriteFile(filepath.Join(target, "user.txt"), []byte("keep"), 0644)
	data := zipFixture(t, map[string]string{"metadata.json": `{"name":"notes"}`})
	if _, err := Materialize(t.Context(), Archive{Name: "notes", Reader: bytes.NewReader(data), Size: int64(len(data))}, target); err == nil {
		t.Fatal("existing target accepted")
	}
	got, _ := os.ReadFile(filepath.Join(target, "user.txt"))
	if string(got) != "keep" {
		t.Fatal("existing project changed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Materialize(ctx, Archive{Name: "notes", Reader: bytes.NewReader(data), Size: int64(len(data))}, filepath.Join(parent, "cancelled")); err == nil {
		t.Fatal("cancelled extraction accepted")
	}
}
