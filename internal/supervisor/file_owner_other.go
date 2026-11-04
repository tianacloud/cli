//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package supervisor

import "os"

func trustedOwner(_ os.FileInfo) bool    { return true }
func trustedWritable(_ os.FileInfo) bool { return false }
