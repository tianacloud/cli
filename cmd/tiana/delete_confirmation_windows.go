//go:build windows

package main

import (
	"context"
	"errors"
	"github.com/tianacloud/cli/internal/consoleinput"
	"io"
	"os"
	"strings"
)

func readDeleteConfirmation(ctx context.Context, input *os.File) (bool, error) {
	output, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err != nil {
		return false, err
	}
	defer output.Close()
	value, err := consoleinput.ReadLine(ctx, input, output, 64)
	if errors.Is(err, io.EOF) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(string(value)))
	return answer == "y" || answer == "yes", nil
}
