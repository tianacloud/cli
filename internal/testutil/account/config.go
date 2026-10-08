// Package account provides isolated account configuration for CLI fixtures.
package account

import (
	"path/filepath"
	"testing"
)

// CredentialPath selects a fresh XDG directory without accessing real accounts.
func CredentialPath(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	return filepath.Join(root, "tiana", "credentials.json")
}
