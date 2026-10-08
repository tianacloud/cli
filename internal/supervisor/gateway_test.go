package supervisor

import (
	"errors"
	"strings"
	"testing"
)

func TestGatewayAddressValidationFailsClosed(t *testing.T) {
	for _, value := range []string{
		"https://192.0.2.10:30080",
		"192.0.2.10",
		"192.0.2.10:0",
		"192.0.2.10:65536",
		"bad_host:443",
		" host.example:443",
		"host.example:443/path",
	} {
		if err := validateGatewayAddress(value); !errors.Is(err, ErrGatewayConfiguration) {
			t.Errorf("validateGatewayAddress(%q) err=%v", value, err)
		}
	}
	for _, value := range []string{"", "192.0.2.10:30080", "gateway.example.test:443", "[2001:db8::1]:443"} {
		if err := validateGatewayAddress(value); err != nil {
			t.Errorf("validateGatewayAddress(%q): %v", value, err)
		}
	}
}

func TestResolveGatewayDialTargetUsesPublishedEndpoint(t *testing.T) {
	hostname := "ep-0" + strings.Repeat("a", 25) + ".db.env123.tiana.test"
	for _, port := range []string{"443", "8445", "18445"} {
		endpoint, err := ParseEndpointURL("https://" + hostname + ":" + port)
		if err != nil {
			t.Fatal(err)
		}
		options, err := resolveGatewayDialTarget(GatewayOptions{}, endpoint)
		if err != nil || options.Address != hostname+":"+port {
			t.Fatalf("options=%+v err=%v", options, err)
		}
	}
}

func TestParseCLIRejectsRemovedAddressAndRepeatedCAOptions(t *testing.T) {
	endpoint := "ep-0" + strings.Repeat("a", 25) + ".db.example.test"
	for _, args := range [][]string{
		{"connect", "--gateway-address=127.0.0.1:443", "--", "turso", "db", "shell", "https://" + endpoint},
		{"connect", "--gateway-ca-der=/a", "--gateway-ca-der", "/b", "--", "turso", "db", "shell", "https://" + endpoint},
	} {
		if _, err := ParseCLI(args); err == nil {
			t.Fatalf("ParseCLI(%q) accepted repeated integration option", args)
		}
	}
}
