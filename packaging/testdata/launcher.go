// A native launcher test fixture, not a distributable Tiana executable.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {
	executable, _ := os.Executable()
	mode, _ := os.ReadFile(filepath.Join(filepath.Dir(executable), "fixture.mode"))
	switch string(mode) {
	case "echo":
		input, _ := io.ReadAll(os.Stdin)
		cwd, _ := os.Getwd()
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"args": os.Args[1:], "input": string(input), "cwd": cwd})
		fmt.Fprintln(os.Stderr, "fixture diagnostic")
		os.Exit(4)
	case "args":
		_ = json.NewEncoder(os.Stdout).Encode(os.Args[1:])
	case "wait":
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
		fmt.Println("ready")
		<-signals
		fmt.Println("interrupted")
		os.Exit(4)
	case "signal":
		process, _ := os.FindProcess(os.Getpid())
		_ = process.Signal(syscall.SIGTERM)
		select {}
	}
}
