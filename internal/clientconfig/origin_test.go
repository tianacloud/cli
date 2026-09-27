package clientconfig

import (
	"strings"
	"testing"
)

func TestParseSavedManagementOrigin(t *testing.T) {
	got, err := ParseManagementOrigin("https://mgr.example.test:9443/")
	if err != nil || got != "https://mgr.example.test:9443" {
		t.Fatalf("origin=%q err=%v", got, err)
	}
	for _, origin := range []string{
		"", "http://mgr.example.test", "https://PRIVATE_SECRET@mgr.example.test",
		"https://mgr.example.test/path", "https://mgr.example.test?PRIVATE_SECRET",
		"https://mgr.example.test#", "https://mgr.example.test:0",
		"https://mgr.example.test:65536", "https://mgr.example.test:", "https://",
		" https://mgr.example.test", "https://mgr.example.test ",
	} {
		if _, err := ParseManagementOrigin(origin); err == nil || strings.Contains(err.Error(), "PRIVATE_SECRET") {
			t.Fatalf("invalid origin accepted or leaked: %v", err)
		}
	}
}
