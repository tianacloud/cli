// Package transportmode carries the invocation's Gateway transport selection.
package transportmode

import (
	"context"
	"errors"
	"os/exec"
	"strings"

	"github.com/tianacloud/cli/internal/netconfig"
	"github.com/tianacloud/sdk-go/gatewaydial"
	"github.com/tianacloud/sdk-go/outbound"
)

type key struct{}
type selection struct {
	mode     gatewaydial.Mode
	explicit bool
}

func Parse(value string) (gatewaydial.Mode, error) {
	switch value {
	case "standard":
		return gatewaydial.Standard, nil
	case "compat":
		return gatewaydial.Compat, nil
	default:
		return gatewaydial.Standard, errors.New("--transport must be standard or compat")
	}
}

func With(ctx context.Context, mode gatewaydial.Mode, explicit bool) context.Context {
	return context.WithValue(ctx, key{}, selection{mode, explicit})
}

func FromContext(ctx context.Context) gatewaydial.Mode {
	s, _ := ctx.Value(key{}).(selection)
	return s.mode
}

func Name(mode gatewaydial.Mode) string {
	if mode == gatewaydial.Compat {
		return "compat"
	}
	return "standard"
}

// GatewayPolicy leaves management networking with netconfig. Compat establishes
// its own direct Endpoint connection and does not initialize unused proxy input.
func GatewayPolicy(ctx context.Context) (*outbound.Policy, error) {
	if FromContext(ctx) == gatewaydial.Compat {
		return nil, nil
	}
	return netconfig.Policy(ctx)
}

// FromGitCommand reads only native Git's invocation-scoped configuration. An
// explicit CLI flag wins; persistent Git configuration does not select a mode.
func FromGitCommand(ctx context.Context) (context.Context, error) {
	if s, _ := ctx.Value(key{}).(selection); s.explicit {
		return ctx, nil
	}
	output, err := exec.CommandContext(ctx, "git", "config", "--show-scope", "--get", "tiana.transport").Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 && len(output) == 0 {
			return ctx, nil
		}
		return ctx, errors.New("cannot read Git command transport selection")
	}
	scope, value, ok := strings.Cut(strings.TrimSuffix(string(output), "\n"), "\t")
	if !ok {
		return ctx, errors.New("invalid Git command transport selection")
	}
	if scope != "command" {
		return ctx, nil
	}
	mode, err := Parse(value)
	if err != nil {
		return ctx, err
	}
	return With(ctx, mode, true), nil
}
