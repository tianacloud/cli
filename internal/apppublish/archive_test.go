package apppublish

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPackLayoutEncodingAndStableIdentity(t *testing.T) {
	d := t.TempDir()
	_ = os.WriteFile(filepath.Join(d, "index.html"), bytes.Repeat([]byte("personal site\n"), 300), 0600)
	_ = os.WriteFile(filepath.Join(d, "photo.png"), []byte{1, 2, 3}, 0600)
	a, hash, e := Pack(d)
	if e != nil {
		t.Fatal(e)
	}
	b, h2, e := Pack(d)
	if e != nil || h2 != hash || !bytes.Equal(a, b) {
		t.Fatal("packing unstable")
	}
	sum := sha256.Sum256(a)
	if hash != hex.EncodeToString(sum[:]) {
		t.Fatal("checksum")
	}
	reader, e := zip.NewReader(bytes.NewReader(a), int64(len(a)))
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range reader.File {
		switch f.Name {
		case "web.yaml":
			if f.UncompressedSize64 != 0 {
				t.Fatal("configuration")
			}
		case "data/index.html":
			if f.Method != zip.Deflate {
				t.Fatal("not compressed")
			}
		case "data/photo.png":
			if f.Method != zip.Store {
				t.Fatal("small file should Store")
			}
		default:
			t.Fatal("unexpected root")
		}
		r, _ := f.Open()
		_, e = io.Copy(io.Discard, r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
	}
}
func TestPackRejectsLinksOversizeAndInvalidPaths(t *testing.T) {
	d := t.TempDir()
	_ = os.Symlink("/etc/passwd", filepath.Join(d, "secret"))
	if _, _, e := Pack(d); e == nil {
		t.Fatal("symlink accepted")
	}
	_ = os.Remove(filepath.Join(d, "secret"))
	f, _ := os.Create(filepath.Join(d, "big"))
	_ = f.Truncate(maxWebBytes + 1)
	f.Close()
	if _, _, e := Pack(d); e == nil {
		t.Fatal("oversize accepted")
	}
	for _, p := range []string{"../a", "a/../b", "a\\b", "a\n", "/a"} {
		if validArchivePath(p) {
			t.Fatal("path accepted")
		}
	}
}
