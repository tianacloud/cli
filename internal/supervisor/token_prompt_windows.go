//go:build windows

package supervisor

import (
	"context"
	"fmt"
	"github.com/tianacloud/cli/internal/consoleinput"
	"io"
	"os"
)

func promptCredential(ctx context.Context) (*SecretToken, error) {
	input, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		return nil, missingCredentialError()
	}
	defer input.Close()
	output, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err != nil {
		return nil, missingCredentialError()
	}
	defer output.Close()
	return readWindowsTerminalCredential(ctx, input, output)
}

func readWindowsTerminalCredential(ctx context.Context, input *os.File, output io.Writer) (*SecretToken, error) {
	if _, err := fmt.Fprint(output, "Enter instance connection Token (input hidden): "); err != nil {
		return nil, err
	}
	defer fmt.Fprintln(output)
	value, err := consoleinput.ReadLine(ctx, input, nil, maxTokenIn)
	if err != nil {
		return nil, err
	}
	defer clear(value)
	if len(value) == 0 {
		return nil, missingCredentialError()
	}
	return ParseToken(value)
}
