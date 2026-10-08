//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package supervisor

import "os"

// Platforms without a portable numeric owner in os.FileInfo still enforce
// regular/non-symlink/0600. Native installer policy may add ACL checks later.
func ownedByCurrentUser(_ os.FileInfo) bool { return true }
func trustedOwner(_ os.FileInfo) bool       { return true }
func trustedWritable(_ os.FileInfo) bool    { return false }
