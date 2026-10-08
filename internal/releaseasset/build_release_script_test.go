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
		name       string
		kernel     string
		machine    string
		goos       string
		goarch     string
		rustTarget string
	}{
		{name: "Linux AMD64", kernel: "Linux", machine: "x86_64", goos: "linux", goarch: "amd64", rustTarget: "x86_64-unknown-linux-musl"},
		{name: "macOS Apple Silicon", kernel: "Darwin", machine: "arm64", goos: "darwin", goarch: "arm64", rustTarget: "aarch64-apple-darwin"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, logPath := newBuildReleaseFixture(t, test.kernel, test.machine)
			expectedMuslCC := ""
			if test.goos == "linux" {
				expectedMuslCC = filepath.Join(fixture, "bin", "x86_64-linux-musl-gcc")
				writeExecutable(t, expectedMuslCC, "#!/bin/sh\nexit 0\n")
			}
			command := exec.Command(filepath.Join(fixture, "cli", "scripts", "build-release.sh"))
			command.Env = append(os.Environ(),
				"PATH="+filepath.Join(fixture, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
				"TEST_BUILD_LOG="+logPath,
				"TEST_SDK_DIR="+filepath.Join(fixture, "sdk"),
				"TEST_EXPECT_MUSL_CC="+expectedMuslCC,
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
				"rustup target add " + test.rustTarget,
				"cargo build --manifest-path " + filepath.Join(fixture, "sdk", "Cargo.toml") + " --locked --release --bin tiana-helper --target " + test.rustTarget,
				"builder " + test.goos + " " + test.goarch + " " + filepath.Join(fixture, "sdk", "target", test.rustTarget, "release", "tiana-helper") + " " + artifact + " version=0.1.0-test insecure=false",
			} {
				if !strings.Contains(log, want+"\n") {
					t.Errorf("build log missing %q:\n%s", want, log)
				}
			}
		})
	}
}

func TestBuildReleaseExplainsMissingLinuxMuslCompiler(t *testing.T) {
	fixture, logPath := newBuildReleaseFixture(t, "Linux", "x86_64")
	if err := os.Symlink("/usr/bin/dirname", filepath.Join(fixture, "bin", "dirname")); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(filepath.Join(fixture, "cli", "scripts", "build-release.sh"))
	command.Env = []string{
		"PATH=" + filepath.Join(fixture, "bin"),
		"TEST_BUILD_LOG=" + logPath,
		"TEST_SDK_DIR=" + filepath.Join(fixture, "sdk"),
	}
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("build without a musl compiler succeeded:\n%s", output)
	}
	if !strings.Contains(string(output), "missing Linux musl C compiler; install musl-tools") {
		t.Fatalf("unexpected diagnostic: %s", output)
	}
}

func TestBuildReleaseRejectsUnsupportedPlatform(t *testing.T) {
	fixture, logPath := newBuildReleaseFixture(t, "Linux", "aarch64")
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
	if !strings.Contains(string(output), "unsupported release host: Linux/aarch64") {
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
	sdk := filepath.Join(fixture, "sdk")
	bin := filepath.Join(fixture, "bin")
	for _, directory := range []string{cliScripts, sdk, bin} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sdk, "Cargo.toml"), []byte("[package]\nname = \"fixture\"\nversion = \"0.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	source, err := os.ReadFile(filepath.Join("..", "..", "scripts", "build-release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cliScripts, "build-release.sh"), source, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(bin, "uname"), "#!/bin/sh\ncase $1 in -s) echo '"+kernel+"';; -m) echo '"+machine+"';; *) exit 2;; esac\n")
	writeExecutable(t, filepath.Join(bin, "rustup"), "#!/bin/sh\n{ printf 'rustup'; printf ' %s' \"$@\"; printf '\\n'; } >>\"$TEST_BUILD_LOG\"\n")
	writeExecutable(t, filepath.Join(bin, "cargo"), `#!/bin/sh
{ printf 'cargo'; printf ' %s' "$@"; printf '\n'; } >>"$TEST_BUILD_LOG"
if [ -n "$TEST_EXPECT_MUSL_CC" ] && [ "${CC_x86_64_unknown_linux_musl:-}" != "$TEST_EXPECT_MUSL_CC" ]; then
  echo 'cargo did not receive the detected musl compiler' >&2
  exit 1
fi
target=
while [ "$#" -gt 0 ]; do
  if [ "$1" = --target ]; then target=$2; break; fi
  shift
done
mkdir -p "$TEST_SDK_DIR/target/$target/release"
: >"$TEST_SDK_DIR/target/$target/release/tiana-helper"
`)
	writeExecutable(t, filepath.Join(cliScripts, "build-single-binary.sh"), `#!/bin/sh
printf 'builder %s %s %s %s version=%s insecure=%s\n' "$1" "$2" "$3" "$4" "$TIANA_RELEASE_VERSION" "$TIANA_INSECURE_TLS" >>"$TEST_BUILD_LOG"
mkdir -p "$(dirname "$4")"
: >"$4"
`)
	return fixture, filepath.Join(fixture, "build.log")
}

func writeExecutable(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
}
