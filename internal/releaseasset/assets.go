package releaseasset

// Assets are immutable public bytes compiled into a single-file release.
// HelperSHA256 is injected by the release build after hashing the exact
// platform helper.
type Assets struct {
	Helper       []byte
	HelperSHA256 string
}

// Available reports whether this build is a self-contained release rather
// than a source-tree development build that expects a hardened installation.
func (a Assets) Available() bool {
	return len(a.Helper) != 0 && a.HelperSHA256 != ""
}
