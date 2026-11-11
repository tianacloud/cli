package transportmode

import (
	"context"
	"os/exec"
	"testing"

	"github.com/tianacloud/sdk-go/gatewaydial"
)

func TestGitCommandSelection(t *testing.T) {
	t.Chdir(t.TempDir())
	if out, err := exec.Command("git", "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	if err := exec.Command("git", "config", "tiana.transport", "compat").Run(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_COUNT", "0")
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	ctx, err := FromGitCommand(context.Background())
	if err != nil || FromContext(ctx) != gatewaydial.Standard {
		t.Fatalf("persistent config changed mode: %v", err)
	}
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "tiana.transport")
	t.Setenv("GIT_CONFIG_VALUE_0", "compat")
	ctx, err = FromGitCommand(context.Background())
	if err != nil || FromContext(ctx) != gatewaydial.Compat {
		t.Fatalf("command config not inherited: %v", err)
	}
	ctx, err = FromGitCommand(With(context.Background(), gatewaydial.Standard, true))
	if err != nil || FromContext(ctx) != gatewaydial.Standard {
		t.Fatalf("explicit flag lost: %v", err)
	}
}
