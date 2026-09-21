package main

import (
	"io"
	"os"

	"golang.org/x/term"
)

func isTerminal(stream any) bool {
	file, ok := stream.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}

func terminalHeight(w io.Writer) (int, bool) {
	file, ok := w.(*os.File)
	if !ok {
		return 0, false
	}
	_, height, err := term.GetSize(int(file.Fd()))
	if err != nil || height <= 0 {
		return 0, false
	}
	return height, true
}

// listPageSize is the interactive page size: terminal height minus three
// fixed lines, clamped to 1..20. A missing height uses the maximum.
func listPageSize(w io.Writer) int {
	height, ok := terminalHeight(w)
	if !ok {
		return 20
	}
	size := height - 3
	if size < 1 {
		size = 1
	}
	if size > 20 {
		size = 20
	}
	return size
}

// interactiveListRequested reports whether the list should page on a
// terminal: both stdio streams are terminals and the caller did not force
// non-interactive output.
func interactiveListRequested(stdin io.Reader, stdout io.Writer, nonInteractive bool) bool {
	return !nonInteractive && isTerminal(stdin) && isTerminal(stdout)
}
