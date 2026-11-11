package main

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/tianacloud/cli/internal/transportmode"
	"github.com/tianacloud/sdk-go/gatewaydial"
	"github.com/urfave/cli/v3"
)

func TestTransportSelectionAndNativeGitArguments(t *testing.T) {
	for _, mode := range []string{"standard", "compat"} {
		for _, name := range []string{"clone", "fetch", "pull", "push", "ls-remote", "submodule"} {
			root := newCLICommand(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, nil)
			args := []string{"--quiet", "origin", "  ref  ", "--help", "--transport=native-value"}
			called := false
			root.Command("git").Command(name).Action = func(ctx context.Context, cmd *cli.Command) error {
				called = true
				if transportmode.Name(transportmode.FromContext(ctx)) != mode || !reflect.DeepEqual(cmd.Args().Slice(), args) {
					t.Fatalf("mode/args changed: %v", cmd.Args().Slice())
				}
				return nil
			}
			invocation := append([]string{"tiana", "--transport", mode, "git", name}, args...)
			if err := root.Run(context.Background(), invocation); err != nil || !called {
				t.Fatalf("%s %s: %v", mode, name, err)
			}
			if passiveUpdateAllowed(invocation[1:]) {
				t.Fatal("native Git schedules passive update")
			}
		}
	}
	var out, diag bytes.Buffer
	called := false
	code := runCLIWithSQL(context.Background(), []string{"--transport", "compat", "sqlite", "shell", "INSTANCE", "-e", "SELECT 1"}, strings.NewReader(""), &out, &diag, func(ctx context.Context, _ sqliteOptions) int {
		called = true
		if transportmode.FromContext(ctx) != gatewaydial.Compat {
			t.Fatal("SQLite lost transport")
		}
		return 0
	})
	if code != 0 || !called {
		t.Fatalf("SQLite %d %s", code, diag.String())
	}
}
