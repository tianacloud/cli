package supervisor

import (
	"bytes"
	"strings"
	"testing"
)

func TestTLSBuildPolicy(t *testing.T) {
	argv := []string{"connect", "--", "turso", "db", "shell", testEndpointURL(t)}
	parsed, err := ParseCLI(argv)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := resolveGatewayTrust(parsed.Connect.Gateway)
	if err != nil || !trust.UseWebPKIRoots {
		t.Fatalf("default trust: %+v, %v", trust, err)
	}
	for _, flag := range []string{"--gateway-ca-der=/tmp/ca.der", "--gateway-ca-der"} {
		if _, err := ParseCLI(append([]string{"connect", flag}, argv[1:]...)); err == nil {
			t.Fatalf("accepted removed option %s", flag)
		}
	}
	debugArgs := append([]string{"connect", "--insecure"}, argv[1:]...)
	parsed, err = ParseCLI(debugArgs)
	if !debugTLSAvailable {
		if err == nil {
			t.Fatal("release accepted insecure")
		}
		if _, err := resolveGatewayTrust(GatewayOptions{InsecureTLS: true}); err == nil {
			t.Fatal("release accepted programmatic insecure")
		}
		if strings.Contains(Usage(), "--insecure") {
			t.Fatal("release advertises insecure")
		}
		return
	}
	if err != nil || !parsed.Connect.Gateway.InsecureTLS {
		t.Fatalf("debug option: %v", err)
	}
	trust, err = resolveGatewayTrust(parsed.Connect.Gateway)
	if err != nil || trust.UseWebPKIRoots {
		t.Fatalf("debug trust: %+v, %v", trust, err)
	}
	if _, err := ParseCLI(append([]string{"connect", "--insecure"}, debugArgs[1:]...)); err == nil {
		t.Fatal("accepted repeated insecure")
	}
}

func TestInsecureConfigWireRoundTrip(t *testing.T) {
	c := HelperConfig{Endpoint: testEndpoint(t).Hostname(), AdapterID: SQLDAdapterID,
		AllowedProfile: []string{ProfileHranaHTTP, ProfileHranaWebSocket}, SelectionMode: SelectionModeBoundedHTTPHeaderClassifier,
		Deadline: SQLDClassifierDeadline, HeaderLimit: SQLDHeaderLimit, FieldLimit: SQLDFieldLimit, Pre200Limit: SQLDPre200Limit, InsecureTLS: true}
	wire := encodeConfig(c)
	got, err := decodeConfig(wire)
	if err != nil || !got.InsecureTLS || got.UseWebPKIRoots || !bytes.Equal(wire, encodeConfig(got)) {
		t.Fatalf("debug config roundtrip: %+v, %v", got, err)
	}
	c.UseWebPKIRoots = true
	if err := validateHelperConfig(c); err == nil {
		t.Fatal("accepted conflicting TLS policy")
	}
	if _, err := decodeConfig(wire[:len(wire)-1]); err == nil {
		t.Fatal("accepted obsolete CONFIG")
	}
}

func TestNativeInsecureFlagDoesNotChangeGatewayTrust(t *testing.T) {
	parsed, err := ParseCLI([]string{"connect", "--", "turso", "db", "shell", testEndpointURL(t), "--insecure"})
	if err != nil || parsed.Connect.Gateway.InsecureTLS || parsed.Connect.NativeArgv[4] != "--insecure" {
		t.Fatalf("native option ownership: %+v, %v", parsed, err)
	}
}
