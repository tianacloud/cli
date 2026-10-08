//go:build !windows

package authclient

import "os"

var writeFixtureFile = os.WriteFile
var chmodFixtureFile = os.Chmod
