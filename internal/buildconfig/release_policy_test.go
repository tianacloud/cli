package buildconfig

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Execute the packaging entrypoints up to their Go invocation. Synthetic tools
// stop before compiling/installing a helper; captures prove effective flags.
func TestReleaseScriptsDefaultToVerifiedTLS(t *testing.T) {
	for _, name := range []string{"build-linux-bundle.sh", "build-single-binary.sh"} {
		for _, setting := range []string{"", "true"} {
			t.Run(name+"/"+setting, func(t *testing.T) {
				root := t.TempDir()
				scripts := filepath.Join(root, "scripts")
				tools := filepath.Join(root, "tools")
				for _, dir := range []string{scripts, tools, filepath.Join(root, "sdk")} {
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
				}
				write := func(path, text string) {
					t.Helper()
					if err := os.WriteFile(path, []byte(text), 0700); err != nil {
						t.Fatal(err)
					}
				}
				data, err := os.ReadFile(filepath.Join("..", "..", "scripts", name))
				if err != nil {
					t.Fatal(err)
				}
				script := filepath.Join(scripts, name)
				write(script, string(data))
				write(filepath.Join(tools, "uname"), "#!/bin/sh\ncase \"$1\" in -s) echo Linux;; -m) echo x86_64;; esac\n")
				write(filepath.Join(tools, "file"), "#!/bin/sh\necho 'ELF 64-bit x86-64'\n")
				fakeGo := filepath.Join(tools, "go-capture")
				write(fakeGo, "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$POLICY_CAPTURE\"\nexit 42\n")
				write(filepath.Join(root, "sdk", "Cargo.toml"), "test fixture")
				helper := filepath.Join(root, "helper")
				write(helper, "test fixture")
				capture := filepath.Join(root, "args")
				t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
				t.Setenv("GO_BIN", fakeGo)
				t.Setenv("POLICY_CAPTURE", capture)
				t.Setenv("TIANA_SDK_RUST_DIR", filepath.Join(root, "sdk"))
				t.Setenv("TIANA_INSECURE_TLS", setting)
				t.Setenv("TIANA_RELEASE_VERSION", "test")
				t.Setenv("TMPDIR", root)
				args := []string{script, filepath.Join(root, "bundle.tar.gz")}
				if name == "build-single-binary.sh" {
					args = []string{script, "linux", "amd64", helper, filepath.Join(root, "tiana")}
				}
				output, err := exec.Command("sh", args...).CombinedOutput()
				if err == nil {
					t.Fatal("build stub did not stop execution")
				}
				flags, err := os.ReadFile(capture)
				if err != nil {
					t.Fatalf("Go invocation not reached: %s: %v", output, err)
				}
				want := "false"
				if setting == "true" {
					want = "true"
				}
				if !strings.Contains(string(flags), "InsecureTLS="+want) {
					t.Fatalf("packaging TLS default/override mismatch: %s", flags)
				}
			})
		}
	}
}
