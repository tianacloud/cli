//go:build !windows

package gitremote

import "os"

var writeFixtureFile = os.WriteFile
var chmodFixtureFile = os.Chmod
