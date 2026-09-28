package sqlitecli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tianacloud/cli/internal/localfile"
)

func TestOutputFileIsPrivateAndExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "中文 output.json")
	file, err := CreateOutput(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("result"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if data, err := localfile.Read(path, 64, true); err != nil || string(data) != "result" {
		t.Fatalf("private output: %q %v", data, err)
	}
	if file, err := CreateOutput(path); err == nil {
		file.Close()
		t.Fatal("replaced existing output")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "result" {
		t.Fatalf("existing output changed: %q %v", data, err)
	}
}
