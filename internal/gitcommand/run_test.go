//go:build !windows

package gitcommand

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tianacloud/cli/internal/clientconfig"
	"github.com/tianacloud/cli/internal/supervisor"
	"github.com/tianacloud/cli/internal/transportmode"
	"github.com/tianacloud/sdk-go/gatewaydial"
)

func TestNativeGitUsesBundledHelperAndInvocationConfig(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "native package")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	helper := `#!/bin/sh
git config --show-scope --get tiana.transport > "$OBSERVED"
printf '%s\n' "$TIANA_CA_FILE" >> "$OBSERVED"
printf '%s\n' "$@" >> "$OBSERVED"
while IFS= read -r line; do
 case "$line" in
 capabilities) printf '\n';;
 list*) printf '0123456789012345678901234567890123456789 refs/heads/main\n\n';;
 '') exit 0;;
 esac
done
`
	if err := os.WriteFile(filepath.Join(dir, "git-remote-tiana"), []byte(helper), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_EXEC_PATH", t.TempDir())
	observed := filepath.Join(t.TempDir(), "observed")
	t.Setenv("OBSERVED", observed)
	t.Setenv("TIANA_CA_FILE", "wrong-ca.pem")
	ctx := transportmode.With(context.Background(), gatewaydial.Compat, true)
	ctx = clientconfig.WithTrust(ctx, &clientconfig.Trust{CAFile: "selected-ca.pem"})
	url := "tiana://ep-01j5c9m7q2v8x4k6n3r0t1w2yz.example.test/repo.git"
	args := []string{"ls-remote", url, "refs/heads/main"}
	command, err := prepare(ctx, filepath.Join(dir, "tiana"), args)
	if err != nil {
		t.Fatal(err)
	}
	var output, diagnostics bytes.Buffer
	code, err := supervisor.RunGitProcess(ctx, command, supervisor.IO{Stdin: strings.NewReader(""), Stdout: &output, Stderr: &diagnostics})
	if err != nil || code != 0 {
		t.Fatalf("code %d: %v %s", code, err, diagnostics.String())
	}
	if !strings.Contains(output.String(), "refs/heads/main") {
		t.Fatal(output.String())
	}
	got, err := os.ReadFile(observed)
	if err != nil {
		t.Fatal(err)
	}
	absolute, _ := filepath.Abs("selected-ca.pem")
	if string(got) != "command\tcompat\n"+absolute+"\n"+url+"\n"+url+"\n" {
		t.Fatalf("helper received %q", got)
	}
}
