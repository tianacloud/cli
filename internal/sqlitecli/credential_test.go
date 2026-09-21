package sqlitecli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testToken = "tia_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func TestTokenStdinPreservesSQL(t *testing.T) {
	r := strings.NewReader(testToken + "\r\nSELECT 1;\n")
	token, e := ReadToken("stdin", "", r)
	if e != nil || token == nil {
		t.Fatal(e)
	}
	rest, _ := io.ReadAll(r)
	if string(rest) != "SELECT 1;\n" {
		t.Fatalf("stole SQL: %q", rest)
	}
}
func TestTokenFileSafety(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(testToken+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, e := ReadToken("file", path, nil); e != nil {
		t.Fatal(e)
	}
	link := path + ".link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, e := ReadToken("file", link, nil); e == nil {
		t.Fatal("accepted symlink")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, e := ReadToken("file", path, nil); e == nil {
		t.Fatal("accepted public file")
	}
}
