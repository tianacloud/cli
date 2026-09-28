//go:build windows

package supervisor

import (
	"strings"
	"testing"

	"github.com/tianacloud/cli/internal/localfile"
)

func TestWindowsConnectCredentialFile(t *testing.T) {
	file, err := localfile.CreateTemp(t.TempDir(), "连接 token-*")
	if err != nil {
		t.Fatal(err)
	}
	value := "tia_" + strings.Repeat("A", 43)
	if _, err := file.WriteString(value + "\r\n"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	token, err := ReadCredential(CredentialSource{Kind: CredentialFromFile, Value: file.Name()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer token.Destroy()
	if string(token.BytesForHandoff()) != value {
		t.Fatal("file credential mismatch")
	}
}
