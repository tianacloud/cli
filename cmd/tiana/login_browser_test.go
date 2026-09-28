package main

import (
	"context"
	"reflect"
	"testing"
)

func TestLoginBrowserKeepsURIAsSingleArgument(t *testing.T) {
	uri := "https://example.test/login?code=abc&return=a%20b"
	for _, tc := range []struct {
		platform string
		args     []string
	}{
		{"linux", []string{"xdg-open", uri}},
		{"darwin", []string{"open", uri}},
		{"windows", []string{"rundll32.exe", "url.dll,FileProtocolHandler", uri}},
	} {
		command := loginBrowserCommand(context.Background(), tc.platform, uri)
		if command == nil || !reflect.DeepEqual(command.Args, tc.args) {
			t.Fatalf("%s browser command=%v", tc.platform, command)
		}
	}
}
