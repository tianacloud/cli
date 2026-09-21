package supervisor

import (
	"fmt"
	"io"
)

func (p SecurityPolicy) Check(actual SecurityLevel, interactive bool, _ io.Writer) error {
	if actual < p.Minimum {
		return fmt.Errorf("%w: actual=%s minimum=%s", ErrSecurityPolicy, actual, p.Minimum)
	}
	if actual != SecurityLoopbackUnisolated {
		return nil
	}
	allowed := p.AllowUnisolated || p.MinimumExplicit
	if !interactive && !allowed {
		return fmt.Errorf("%w: loopback_unisolated requires an explicit allow policy", ErrSecurityPolicy)
	}
	// An interactive invocation has a documented allow default. Non-interactive
	// callers must opt in with --allow-unisolated-loopback or an explicit
	// minimum-security policy.
	return nil
}
