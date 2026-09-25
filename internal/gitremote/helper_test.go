package gitremote

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/supervisor"
)

const testHost = "ep-00000000000000000000000000.db.example.test"

func testRepo(t *testing.T) supervisor.Endpoint {
	t.Helper()
	r, e := parseRepository("tiana://" + testHost + "/repo.git")
	if e != nil {
		t.Fatal(e)
	}
	return r
}

type pipeTunnel struct {
	read  *io.PipeReader
	write *io.PipeWriter
}

func (p pipeTunnel) Read(b []byte) (int, error)  { return p.read.Read(b) }
func (p pipeTunnel) Write(b []byte) (int, error) { return p.write.Write(b) }
func (p pipeTunnel) CloseWrite() error           { return p.write.Close() }
func (p pipeTunnel) Close() error                { p.write.Close(); return p.read.Close() }

func TestRepositoryValidation(t *testing.T) {
	for _, scheme := range []string{"tiana://", "https://"} {
		r, e := parseRepository(scheme + testHost + ":30080/repo.git")
		if e != nil || r.Port() != "30080" || r.Hostname() != testHost {
			t.Fatalf("deployed endpoint: %v", e)
		}
	}
	for _, s := range []string{"tiana://secret@" + testHost + "/repo.git", "tiana://" + testHost + ":0/repo.git", "tiana://" + testHost + ":0443/repo.git", "tiana://" + testHost + "/repo.git?token=secret", "tiana://" + testHost + "/%72epo.git", "tiana://" + testHost + "/../repo.git", "tiana://" + testHost + "/repo.git#x", "tiana://127.0.0.1/repo.git"} {
		if _, e := parseRepository(s); e == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
func TestPrefetchedBytesAndHalfClose(t *testing.T) {
	for _, service := range []string{"git-upload-pack", "git-receive-pack"} {
		t.Run(service, func(t *testing.T) {
			remoteInput, upload := io.Pipe()
			download, remoteOutput := io.Pipe()
			stream := pipeTunnel{download, upload}
			defer remoteInput.Close()
			defer remoteOutput.Close()
			received := make(chan []byte, 1)
			go func() {
				b, _ := io.ReadAll(remoteInput)
				received <- b
				remoteOutput.Write([]byte("\xfffinal-after-client-eof\x00"))
				remoteOutput.Close()
			}()
			input := io.NopCloser(strings.NewReader("capabilities\nconnect " + service + "\n\x00pack\xff"))
			var output bytes.Buffer
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if e := run(ctx, input, &output, testRepo(t), func(context.Context, supervisor.Endpoint) (tunnel, error) { return stream, nil }); e != nil {
				t.Fatal(e)
			}
			body := <-received
			want := service + " /repo.git\x00host=" + testHost + "\x00\x00pack\xff"
			if string(body[4:]) != want {
				t.Fatalf("data changed: %q", body)
			}
			if output.String() != "connect\n\n\n\xfffinal-after-client-eof\x00" {
				t.Fatalf("output changed: %q", output.String())
			}
		})
	}
}
func TestRemoteEOFFinishesWithInputOpen(t *testing.T) {
	input, git := io.Pipe()
	defer git.Close()
	remoteInput, upload := io.Pipe()
	download, remoteOutput := io.Pipe()
	defer remoteInput.Close()
	go func() { io.WriteString(git, "capabilities\nconnect git-upload-pack\n") }()
	go func() { b := make([]byte, 1024); remoteInput.Read(b); remoteOutput.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e := run(ctx, input, io.Discard, testRepo(t), func(context.Context, supervisor.Endpoint) (tunnel, error) { return pipeTunnel{download, upload}, nil }); e != nil {
		t.Fatal(e)
	}
}
func TestRejectedSetupNeverSucceeds(t *testing.T) {
	var output bytes.Buffer
	err := run(context.Background(), io.NopCloser(strings.NewReader("capabilities\nconnect git-upload-pack\nsecret-pack")), &output, testRepo(t), func(context.Context, supervisor.Endpoint) (tunnel, error) { return nil, errors.New("denied") })
	if err == nil || output.String() != "connect\n\n" {
		t.Fatalf("invalid rejection: %v %q", err, output.String())
	}
}

func TestCancelledGitConnectionReportsRequestID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	go func() { <-started; cancel() }()
	err := run(ctx, io.NopCloser(strings.NewReader("capabilities\nconnect git-upload-pack\n")), io.Discard, testRepo(t), func(ctx context.Context, _ supervisor.Endpoint) (tunnel, error) {
		ctx.Value(requestIdentityKey{}).(*requestIdentity).set("req-cancelled-git")
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err == nil || !strings.Contains(err.Error(), "req-cancelled-git") {
		t.Fatalf("cancelled Git operation has no diagnostic ID: %v", err)
	}
}
func TestInvalidCommandsNeverConnect(t *testing.T) {
	for _, s := range []string{"connect git-upload-archive\n", "connect git-upload-pack\r\n", "unterminated", strings.Repeat("x", maxCommand) + "\n", "connect git-upload-pack\x00\n"} {
		err := run(context.Background(), io.NopCloser(strings.NewReader("capabilities\n"+s)), io.Discard, testRepo(t), func(context.Context, supervisor.Endpoint) (tunnel, error) {
			t.Error("unexpected connection")
			return nil, errors.New("unexpected")
		})
		if err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
