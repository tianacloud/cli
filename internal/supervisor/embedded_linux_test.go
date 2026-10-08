//go:build linux

package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestEmbeddedHelperMemfdIsSealed(t *testing.T) {
	image, err := os.ReadFile("/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	command, cleanup, err := embeddedHelperCommand(context.Background(), image)
	if err != nil {
		t.Fatalf("embeddedHelperCommand: %v", err)
	}
	defer cleanup()
	if len(command.ExtraFiles) != 1 || command.Path != "/proc/self/fd/3" {
		t.Fatalf("command path=%q extra-files=%d", command.Path, len(command.ExtraFiles))
	}
	seals, err := unix.FcntlInt(command.ExtraFiles[0].Fd(), unix.F_GET_SEALS, 0)
	if err != nil {
		t.Fatalf("F_GET_SEALS: %v", err)
	}
	want := unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
	if seals&want != want {
		t.Fatalf("memfd seals=%#x want=%#x", seals, want)
	}
}

func TestEmbeddedHelperExecutesWithoutInstalledCompanion(t *testing.T) {
	image, err := os.ReadFile("/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(image)
	launcher, err := NewEmbeddedHelperLauncher(image, hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	client, err := launcher.Launch(context.Background(), CredentialSource{})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	process, ok := client.(*ProcessHelper)
	if !ok {
		t.Fatalf("client=%T", client)
	}
	select {
	case err := <-process.Done():
		if err != nil {
			t.Fatalf("embedded /bin/true exit: %v", err)
		}
	case <-time.After(2 * time.Second):
		_ = process.Close()
		t.Fatal("embedded helper did not exit")
	}
	_ = process.Close()
}
