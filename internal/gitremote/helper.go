// Package gitremote implements the native Git remote-helper in the Go CLI.
package gitremote

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/tianacloud/cli/internal/supervisor"
)

const maxCommand = 4096

type tunnel interface {
	io.ReadWriteCloser
	CloseWrite() error
}
type connector func(context.Context, supervisor.Endpoint) (tunnel, error)

type requestIdentityKey struct{}
type requestIdentity struct {
	mu sync.Mutex
	id string
}

func (i *requestIdentity) set(id string) { i.mu.Lock(); i.id = id; i.mu.Unlock() }
func (i *requestIdentity) get() string   { i.mu.Lock(); defer i.mu.Unlock(); return i.id }

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
	identity := &requestIdentity{}
	ctx = context.WithValue(ctx, requestIdentityKey{}, identity)
	defer input.Close()
	result := make(chan error, 1)
	go func() { result <- negotiate(ctx, input, output, repo, dial) }()
	select {
	case err := <-result:
		return gitConnectionError(err, identity.get())
	case <-ctx.Done():
		return gitConnectionError(errors.New("interrupted; operation outcome may be unknown"), identity.get())
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
			requestID := ""
			if diagnostic, ok := stream.(interface{ RequestID() string }); ok {
				requestID = diagnostic.RequestID()
			}
			payload := strings.TrimPrefix(command, "connect ") + " /repo.git\x00host=" + repo.Hostname() + "\x00"
			if _, err = fmt.Fprintf(stream, "%04x%s", len(payload)+4, payload); err != nil {
				return gitConnectionError(errors.New("Git daemon request failed"), requestID)
			}
			if _, err = io.WriteString(output, "\n"); err != nil {
				return err
			}
			return gitConnectionError(relay(ctx, reader, output, stream), requestID)
		default:
			return errors.New("unsupported Git remote-helper command")
		}
	}
}

func gitConnectionError(err error, requestID string) error {
	if err == nil || requestID == "" {
		return err
	}
	var existing *connectionError
	if errors.As(err, &existing) {
		return err
	}
	return &connectionError{cause: err, requestID: requestID}
}

type connectionError struct {
	cause     error
	requestID string
}

func (e *connectionError) Error() string {
	return fmt.Sprintf("%s (request ID %s)", e.cause, e.requestID)
}
func (e *connectionError) Unwrap() error { return e.cause }

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
