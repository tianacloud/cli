//go:build linux || darwin

package releaseasset

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildReleaseBuildsNativeSupportedPlatform(t *testing.T) {
	tests := []struct {
		name    string
		kernel  string
		machine string
		goos    string
		goarch  string
	}{
		{name: "Linux AMD64", kernel: "Linux", machine: "x86_64", goos: "linux", goarch: "amd64"},
		{name: "macOS Apple Silicon", kernel: "Darwin", machine: "arm64", goos: "darwin", goarch: "arm64"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, logPath := newBuildReleaseFixture(t, test.kernel, test.machine)
			command := exec.Command(filepath.Join(fixture, "cli", "scripts", "build-release.sh"))
			command.Env = append(os.Environ(),
				"PATH="+filepath.Join(fixture, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
				"TEST_BUILD_LOG="+logPath,
				"TIANA_RELEASE_VERSION=0.1.0-test",
			)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("build-release.sh failed: %v\n%s", err, output)
			}

			artifact := filepath.Join(fixture, "cli", "dist", "tiana-cli-"+test.goos+"-"+test.goarch+"-0.1.0-test")
			if _, err := os.Stat(artifact); err != nil {
				t.Fatalf("release artifact missing: %v", err)
			}
			logBytes, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			log := string(logBytes)
			for _, want := range []string{
				"builder " + test.goos + " " + test.goarch + " " + artifact + " version=0.1.0-test insecure=false",
			} {
				if !strings.Contains(log, want+"\n") {
					t.Errorf("build log missing %q:\n%s", want, log)
				}
			}
		})
	}
}

func TestBuildReleaseRejectsUnsupportedPlatform(t *testing.T) {
	fixture, logPath := newBuildReleaseFixture(t, "Linux", "unknown")
	command := exec.Command(filepath.Join(fixture, "cli", "scripts", "build-release.sh"))
	command.Env = append(os.Environ(),
		"PATH="+filepath.Join(fixture, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"TEST_BUILD_LOG="+logPath,
		"TEST_SDK_DIR="+filepath.Join(fixture, "sdk"),
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("unsupported platform succeeded:\n%s", output)
	}
	if !strings.Contains(string(output), "unsupported release host: Linux/unknown") {
		t.Fatalf("unexpected diagnostic: %s", output)
	}
	if data, err := os.ReadFile(logPath); err == nil && len(data) != 0 {
		t.Fatalf("toolchain ran for unsupported platform: %s", data)
	}
}

func newBuildReleaseFixture(t *testing.T, kernel, machine string) (string, string) {
	t.Helper()
	fixture, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cliScripts := filepath.Join(fixture, "cli", "scripts")
	bin := filepath.Join(fixture, "bin")
	for _, directory := range []string{cliScripts, bin} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	source, err := os.ReadFile(filepath.Join("..", "..", "scripts", "build-release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cliScripts, "build-release.sh"), source, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(bin, "uname"), "#!/bin/sh\ncase $1 in -s) echo '"+kernel+"';; -m) echo '"+machine+"';; *) exit 2;; esac\n")
	for _, tool := range []string{"cargo", "rustup", "musl-gcc"} {
		writeExecutable(t, filepath.Join(bin, tool), "#!/bin/sh\necho 'unexpected legacy toolchain' >&2\nexit 99\n")
	}
	writeExecutable(t, filepath.Join(cliScripts, "build-single-binary.sh"), `#!/bin/sh
printf 'builder %s %s %s version=%s insecure=%s\n' "$1" "$2" "$3" "$TIANA_RELEASE_VERSION" "$TIANA_INSECURE_TLS" >>"$TEST_BUILD_LOG"
mkdir -p "$(dirname "$3")"
: >"$3"
`)
	return fixture, filepath.Join(fixture, "build.log")
}

func writeExecutable(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
}
