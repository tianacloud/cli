//go:build !windows

package sqlitecli

import "os"

var writeFixtureFile = os.WriteFile
var chmodFixtureFile = os.Chmod
