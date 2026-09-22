package supervisor

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestStdinCredentialConsumesExactlyOneLine(t *testing.T) {
	tokenValue := "tia_" + strings.Repeat("A", 43)
	input := strings.NewReader(tokenValue + "\r\nSELECT 1;\n")
	token, err := ReadCredential(CredentialSource{Kind: CredentialFromStdin}, input)
	if err != nil {
		t.Fatalf("ReadCredential: %v", err)
	}
	defer token.Destroy()
	if got := string(token.BytesForHandoff()); got != tokenValue {
		t.Fatalf("token=%q", got)
	}
	remaining, err := io.ReadAll(input)
	if err != nil {
		t.Fatalf("read remaining stdin: %v", err)
	}
	if got := string(remaining); got != "SELECT 1;\n" {
		t.Fatalf("remaining stdin=%q", got)
	}
}

func TestStdinCredentialAcceptsEOFWithoutNewline(t *testing.T) {
	tokenValue := "tia_" + strings.Repeat("A", 43)
	token, err := ReadCredential(
		CredentialSource{Kind: CredentialFromStdin},
		strings.NewReader(tokenValue),
	)
	if err != nil {
		t.Fatalf("ReadCredential: %v", err)
	}
	defer token.Destroy()
	if got := string(token.BytesForHandoff()); got != tokenValue {
		t.Fatalf("token=%q", got)
	}
}

func TestStdinCredentialRejectsOversizedFirstLine(t *testing.T) {
	_, err := ReadCredential(
		CredentialSource{Kind: CredentialFromStdin},
		strings.NewReader(strings.Repeat("x", maxTokenIn+1)+"\nSELECT 1;\n"),
	)
	if err == nil || errors.Is(err, ErrInvalidToken) {
		t.Fatalf("oversized stdin err=%v", err)
	}
}

func TestConnectCredentialSourcesDoNotFallBack(t *testing.T) {
	t.Setenv("TIANA_TOKEN", "tia_"+strings.Repeat("A", 43))
	for _, tc := range []struct {
		name     string
		source   CredentialSource
		explicit bool
		value    string
	}{
		{"default empty", DefaultCredentialSource(), false, ""},
		{"default invalid", DefaultCredentialSource(), false, "invalid"},
		{"explicit missing env", CredentialSource{Kind: CredentialFromEnvironment, Value: "TIANA_TEST_MISSING"}, true, ""},
		{"missing file", CredentialSource{Kind: CredentialFromFile, Value: t.TempDir() + "/absent"}, true, ""},
		{"empty stdin", CredentialSource{Kind: CredentialFromStdin}, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TIANA_TOKEN", tc.value)
			t.Setenv("TIANA_TEST_MISSING", "")
			os.Unsetenv("TIANA_TEST_MISSING")
			token, err := readConnectCredential(context.Background(), ConnectOptions{Credential: tc.source, TokenSourceSet: tc.explicit, Interactive: true}, strings.NewReader(""))
			if err == nil || token != nil {
				t.Fatal("missing or invalid credentials accepted")
			}
		})
	}
	t.Run("noninteractive absent", func(t *testing.T) {
		os.Unsetenv("TIANA_TOKEN")
		_, err := readConnectCredential(context.Background(), ConnectOptions{Credential: DefaultCredentialSource()}, nil)
		if err == nil || !strings.Contains(err.Error(), "TIANA_TOKEN") || strings.Contains(err.Error(), "--token-file") {
			t.Fatalf("missing actionable error: %v", err)
		}
	})
}

func TestConnectExplicitStdinOverridesEnvironment(t *testing.T) {
	t.Setenv("TIANA_TOKEN", "invalid")
	input := strings.NewReader("tia_" + strings.Repeat("A", 43) + "\nSELECT 1;\n")
	token, err := readConnectCredential(context.Background(), ConnectOptions{Credential: CredentialSource{Kind: CredentialFromStdin}, TokenSourceSet: true}, input)
	if err != nil {
		t.Fatal(err)
	}
	defer token.Destroy()
	rest, _ := io.ReadAll(input)
	if string(rest) != "SELECT 1;\n" {
		t.Fatal("native stdin consumed")
	}
}
