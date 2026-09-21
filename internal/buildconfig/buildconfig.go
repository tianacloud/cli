// Package buildconfig carries release settings injected by the build scripts
// with -ldflags -X. Source builds keep the strict defaults.
package buildconfig

import "strings"

// InsecureTLS accepts untrusted server certificates for MGR requests and the
// Gateway tunnel when a build sets it to "true". The source default verifies
// every certificate chain.
var InsecureTLS = "false"

// TLSInsecure reports whether this build accepts untrusted server
// certificates.
func TLSInsecure() bool {
	return strings.EqualFold(strings.TrimSpace(InsecureTLS), "true")
}
