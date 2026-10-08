package main

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/urfave/cli/v3"
)

func createMessageOption() cli.Flag {
	return &cli.StringFlag{Name: "message", Aliases: []string{"m"}, Usage: "Instance description (UTF-8, at most 2048 bytes)", Local: true, Validator: func(value string) error {
		if len(value) > 2048 || !utf8.ValidString(value) {
			return errors.New("description must be valid UTF-8 and at most 2048 bytes")
		}
		return nil
	}}
}

// Description values are not positional names. In particular, a description
// equal to a whitespace-padded name must not trigger the name-trimming guard.
func instanceCreateNameWasTrimmed(cmd *cli.Command, value string) bool {
	args := leafArguments(cmd)
	for i := 0; i < len(args); i++ {
		raw := args[i]
		if raw == "--" {
			return false
		} // Values after -- retain their original whitespace.
		if raw == "-m" || raw == "--message" {
			i++
			continue
		}
		if strings.HasPrefix(raw, "-m=") || strings.HasPrefix(raw, "--message=") {
			continue
		}
		if raw != value && strings.TrimSpace(raw) == value {
			return true
		}
	}
	return false
}
