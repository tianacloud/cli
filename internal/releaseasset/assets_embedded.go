//go:build tiana_embedded

package releaseasset

import _ "embed"

// helperSHA256 is set with -ldflags by the release builder. Keeping the
// expected digest outside the embedded payload detects build/staging mistakes
// before any helper process is created.
var helperSHA256 string

//go:embed generated/tiana-helper
var helper []byte

func Current() Assets {
	return Assets{
		Helper:       helper,
		HelperSHA256: helperSHA256,
	}
}
