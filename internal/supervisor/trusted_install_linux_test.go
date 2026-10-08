//go:build linux

package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const replacementFixtureUID = 65534

func TestUserOwnedInstallChainIsRejected(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root cannot create a user-owned installation fixture")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "libexec", "tiana"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(root, "libexec", "tiana"), 0o755)
		_ = os.Chmod(filepath.Join(root, "libexec"), 0o755)
		_ = os.Chmod(root, 0o755)
	})
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "libexec"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "libexec", "tiana"), 0o555); err != nil {
		t.Fatal(err)
	}
	if trustedInstallChain(root) {
		t.Fatal("user-owned read-only install chain was trusted")
	}
}

func TestRootManagedInstallReplacementFixture(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires a root test container to create the platform-managed fixture")
	}

	root, err := os.MkdirTemp("/usr/local", ".tiana-root-fixture-")
	if err != nil {
		t.Skipf("cannot create root-managed fixture under /usr/local: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	tianaDir := filepath.Join(root, "libexec", "tiana")
	if err := os.MkdirAll(tianaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "libexec"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tianaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if !trustedInstallChain(root) {
		t.Fatal("standard root-owned install chain was rejected")
	}

	attackerParent := t.TempDir()
	if err := os.Chmod(attackerParent, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(attackerParent, replacementFixtureUID, replacementFixtureUID); err != nil {
		t.Fatal(err)
	}
	attackerRoot := filepath.Join(attackerParent, "managed")
	if err := os.MkdirAll(filepath.Join(attackerRoot, "libexec", "tiana"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !trustedInstallChain(attackerRoot) {
		// Expected: the writable attacker parent is part of the checked chain.
	} else {
		t.Fatal("root-owned tree under an attacker-writable parent was trusted")
	}

	symlinkBase, err := os.MkdirTemp("/usr/local", ".tiana-symlink-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(symlinkBase) })
	realParent := filepath.Join(symlinkBase, "real")
	linkParent := filepath.Join(symlinkBase, "link")
	symlinkRoot := filepath.Join(linkParent, "managed")
	if err := os.MkdirAll(filepath.Join(realParent, "managed", "libexec", "tiana"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realParent, linkParent); err != nil {
		t.Fatal(err)
	}
	if !trustedInstallChain(symlinkRoot) {
		// Expected: an ancestor symlink is not a stable pathname boundary.
	} else {
		t.Fatal("install chain with an ancestor symlink was trusted")
	}

	launcherBytes, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	launcherPath := filepath.Join(root, "launcher-fixture.test")
	if err := os.WriteFile(launcherPath, launcherBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(launcherPath, 0o755); err != nil {
		t.Fatal(err)
	}

	markerDir := filepath.Join(root, "marker")
	if err := os.Mkdir(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(markerDir, replacementFixtureUID, replacementFixtureUID); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(markerDir, "started")
	helperPath := filepath.Join(tianaDir, "tiana-helper")
	helperBytes := []byte("#!/bin/sh\nprintf 'manifest-helper' > " + shellQuote(marker) + "\n")
	if err := os.WriteFile(helperPath, helperBytes, 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(helperPath, 0o555); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(helperBytes)
	manifest := helperManifest{
		ContractVersion:    HelperContractVersion,
		HelperRelativePath: trustedHelperRelativePath,
		SHA256:             hex.EncodeToString(digest[:]),
		Platform:           "linux",
		Arch:               runtime.GOARCH,
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(tianaDir, trustedManifestName)
	if err := os.WriteFile(manifestPath, manifestBytes, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(manifestPath, 0o444); err != nil {
		t.Fatal(err)
	}

	trapDir := filepath.Join(root, "trap")
	if err := os.Mkdir(trapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	trap := filepath.Join(trapDir, "tiana-helper")
	trapBytes := []byte("#!/bin/sh\nprintf 'path-trap' > " + shellQuote(marker) + "\n")
	if err := os.WriteFile(trap, trapBytes, 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(trap, 0o555); err != nil {
		t.Fatal(err)
	}

	child := exec.Command(launcherPath, "-test.run=^TestRootManagedInstallReplacementChild$")
	child.Env = append(os.Environ(),
		"TIANA_ROOT_FIXTURE="+root,
		"TIANA_MARKER="+marker,
		"TIANA_TRAP_DIR="+trapDir,
	)
	child.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{
		Uid:         replacementFixtureUID,
		Gid:         replacementFixtureUID,
		NoSetGroups: true,
	}}
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("non-root launcher fixture failed: %v\n%s", err, output)
	}
	content, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "manifest-helper" {
		t.Fatalf("started helper=%q, want manifest-helper", content)
	}
}

func TestRootManagedInstallReplacementChild(t *testing.T) {
	root := os.Getenv("TIANA_ROOT_FIXTURE")
	if root == "" {
		return
	}
	if os.Geteuid() == 0 {
		t.Fatal("replacement fixture child did not drop privileges")
	}
	helperPath := filepath.Join(root, filepath.FromSlash(trustedHelperRelativePath))
	manifestPath := filepath.Join(root, "libexec", "tiana", trustedManifestName)
	stop := make(chan struct{})
	var replacementSuccesses atomic.Int32
	var attemptCount atomic.Int32
	var waitGroup sync.WaitGroup
	waitGroup.Add(1)
	attackerReady := make(chan struct{})
	go func() {
		defer waitGroup.Done()
		firstBatch := true
		for {
			chmodErr := os.Chmod(helperPath, 0o777)
			attemptCount.Add(1)
			if chmodErr == nil {
				replacementSuccesses.Add(1)
			}
			writeErr := os.WriteFile(helperPath, []byte("replacement"), 0o777)
			attemptCount.Add(1)
			if writeErr == nil {
				replacementSuccesses.Add(1)
			}
			renameHelperErr := os.Rename(helperPath, helperPath+".replacement")
			attemptCount.Add(1)
			if renameHelperErr == nil {
				replacementSuccesses.Add(1)
			}
			renameManifestErr := os.Rename(manifestPath, manifestPath+".replacement")
			attemptCount.Add(1)
			if renameManifestErr == nil {
				replacementSuccesses.Add(1)
			}
			if firstBatch {
				firstBatch = false
				close(attackerReady)
			}
			select {
			case <-stop:
				return
			default:
			}
		}
	}()
	<-attackerReady

	if err := os.Chdir(os.Getenv("TIANA_TRAP_DIR")); err != nil {
		close(stop)
		waitGroup.Wait()
		t.Fatal(err)
	}
	if err := os.Setenv("PATH", os.Getenv("TIANA_TRAP_DIR")); err != nil {
		close(stop)
		waitGroup.Wait()
		t.Fatal(err)
	}
	trusted, err := ResolveTrustedHelper(root)
	if err != nil {
		close(stop)
		waitGroup.Wait()
		t.Fatalf("ResolveTrustedHelper: %v", err)
	}
	client, err := (ProcessHelperLauncher{Helper: trusted}).Launch(context.Background(), CredentialSource{})
	if err != nil {
		close(stop)
		waitGroup.Wait()
		t.Fatalf("Launch: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		content, readErr := os.ReadFile(os.Getenv("TIANA_MARKER"))
		if readErr == nil && string(content) == "manifest-helper" {
			break
		}
		if time.Now().After(deadline) {
			_ = client.Close()
			close(stop)
			waitGroup.Wait()
			t.Fatalf("manifest helper did not write marker before bounded deadline: read=%v content=%q", readErr, content)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := client.Close(); err != nil {
		close(stop)
		waitGroup.Wait()
		t.Fatalf("Close: %v", err)
	}
	close(stop)
	waitGroup.Wait()
	if count := attemptCount.Load(); count == 0 {
		t.Fatal("replacement attacker did not run")
	}
	if count := replacementSuccesses.Load(); count != 0 {
		t.Fatalf("non-root replacement attempts succeeded: %d", count)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
