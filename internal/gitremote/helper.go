// Package gitremote implements the native Git remote-helper in the Go CLI.
package gitremote

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/tianacloud/cli/internal/supervisor"
)

const maxCommand = 4096

type tunnel interface {
	io.ReadWriteCloser
	CloseWrite() error
}
type connector func(context.Context, supervisor.Endpoint) (tunnel, error)

func parseRepository(raw string) (supervisor.Endpoint, error) {
	authority := strings.TrimPrefix(raw, "tiana://")
	if authority == raw {
		authority = strings.TrimPrefix(raw, "https://")
	}
	if authority == raw || !strings.HasSuffix(authority, "/repo.git") {
		return supervisor.Endpoint{}, errors.New("expected tiana://<endpoint>[:port]/repo.git")
	}
	authority = strings.TrimSuffix(authority, "/repo.git")
	if strings.ContainsAny(authority, "/?#@%") {
		return supervisor.Endpoint{}, errors.New("invalid Git Endpoint URL")
	}
	return supervisor.ParseEndpointURL("https://" + authority)
}

// Run owns input for this one-shot command. Closing it unblocks negotiation or
// upload on cancellation/remote EOF; main exits after this command returns.
func Run(ctx context.Context, args []string, input io.ReadCloser, output io.Writer) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("usage: tiana git remote-helper <remote> [tiana://<endpoint>[:port]/repo.git]")
	}
	repo, err := parseRepository(args[len(args)-1])
	if err != nil {
		return err
	}
	return run(ctx, input, output, repo, connect)
}

func run(ctx context.Context, input io.ReadCloser, output io.Writer, repo supervisor.Endpoint, dial connector) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer input.Close()
	result := make(chan error, 1)
	go func() { result <- negotiate(ctx, input, output, repo, dial) }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return errors.New("interrupted; operation outcome may be unknown")
	}
}

func negotiate(ctx context.Context, input io.Reader, output io.Writer, repo supervisor.Endpoint, dial connector) error {
	reader := bufio.NewReaderSize(input, maxCommand)
	capabilities := false
	for {
		line, err := reader.ReadSlice('\n')
		if err == io.EOF && len(line) == 0 {
			return nil
		}
		if err != nil || len(line) > maxCommand || strings.ContainsAny(string(line), "\x00\r") {
			return errors.New("invalid or oversized Git helper command")
		}
		command := string(line[:len(line)-1])
		switch {
		case command == "":
			return nil
		case command == "capabilities" && !capabilities:
			if _, err := io.WriteString(output, "connect\n\n"); err != nil {
				return err
			}
			capabilities = true
		case capabilities && (command == "connect git-upload-pack" || command == "connect git-receive-pack"):
			stream, err := dial(ctx, repo)
			if err != nil {
				return err
			}
			defer stream.Close()
			payload := strings.TrimPrefix(command, "connect ") + " /repo.git\x00host=" + repo.Hostname() + "\x00"
			if _, err = fmt.Fprintf(stream, "%04x%s", len(payload)+4, payload); err != nil {
				return errors.New("Git daemon request failed")
			}
			if _, err = io.WriteString(output, "\n"); err != nil {
				return err
			}
			return relay(ctx, reader, output, stream)
		default:
			return errors.New("unsupported Git remote-helper command")
		}
	}
}

func relay(ctx context.Context, input io.Reader, output io.Writer, stream tunnel) error {
	upload, download := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := io.Copy(stream, input)
		if err == nil {
			err = stream.CloseWrite()
		}
		upload <- err
	}()
	go func() { _, err := io.Copy(output, stream); download <- err }()
	select {
	case err := <-upload:
		if err != nil {
			return errors.New("Git upload transport failed; operation outcome may be unknown")
		}
		select {
		case err = <-download:
		case <-ctx.Done():
			return ctx.Err()
		}
		if err != nil {
			return errors.New("Git download transport failed")
		}
		return nil
	case err := <-download:
		if err != nil {
			return errors.New("Git download transport failed")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
