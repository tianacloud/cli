// This executable is only a packaging fixture, never a Tiana release asset.
package main

import (
	"fmt"
	"runtime"
)

func main() { fmt.Printf("packaging fixture %s/%s\n", runtime.GOOS, runtime.GOARCH) }
