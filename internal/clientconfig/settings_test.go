package clientconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPublicSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"managementOrigin":"https://mgr.example.test:9443/"}`), 0644); err != nil {
		t.Fatal(err)
	}
	settings, err := LoadSettings(path)
	if err != nil || settings.ManagementOrigin != "https://mgr.example.test:9443" {
		t.Fatalf("settings=%+v err=%v", settings, err)
	}
}

func TestLoadSettingsRejectsInvalidExplicitConfiguration(t *testing.T) {
	for _, input := range []string{
		``, `not-json PRIVATE_SECRET`, `null`, `{}`, `[]`,
		`{"managementOrigin":12}`, `{"managementOrigin":"https://mgr.example.test","unknown":"PRIVATE_SECRET"}`,
		`{"managementOrigin":"https://mgr.example.test"} {}`, `{"managementOrigin":"http://mgr.example.test"}`,
		`{"managementOrigin":"https://PRIVATE_SECRET@mgr.example.test"}`, `{"managementOrigin":"https://mgr.example.test/path"}`,
		`{"managementOrigin":"https://mgr.example.test?PRIVATE_SECRET"}`, `{"managementOrigin":"https://mgr.example.test#"}`,
		`{"managementOrigin":"https://mgr.example.test:0"}`, `{"managementOrigin":"https://mgr.example.test:65536"}`,
		`{"managementOrigin":"https://mgr.example.test:"}`, `{"managementOrigin":"https://"}`,
		strings.Repeat(" ", (64<<10)+1),
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(input), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadSettings(path); err == nil || strings.Contains(err.Error(), "PRIVATE_SECRET") {
			t.Fatalf("unexpected result for configuration: %v", err)
		}
	}
	if _, err := LoadSettings(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing explicit configuration accepted")
	}
}
