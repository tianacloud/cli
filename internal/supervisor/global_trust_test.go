package supervisor

import (
	"github.com/tianacloud/cli/internal/buildconfig"
	"net/http/httptest"
	"testing"
)

func TestGlobalTrustForcesVerifiedHelperTLS(t *testing.T) {
	s := httptest.NewTLSServer(nil)
	defer s.Close()
	old := buildconfig.InsecureTLS
	buildconfig.InsecureTLS = "true"
	defer func() { buildconfig.InsecureTLS = old }()
	trust, err := resolveGatewayTrust(GatewayOptions{CAFile: "/missing/ca", CACertificates: [][]byte{s.Certificate().Raw}})
	if err != nil || trust.InsecureTLS || !trust.UseWebPKIRoots || len(trust.RootCertDER) != 1 {
		t.Fatal("global trust not applied", err)
	}
}

func TestHelperUsesSystemTrustWithoutExtraRoots(t *testing.T) {
	trust, err := resolveGatewayTrust(GatewayOptions{})
	if err != nil || trust.InsecureTLS || !trust.UseWebPKIRoots || len(trust.RootCertDER) != 0 {
		t.Fatalf("system trust changed: %+v %v", trust, err)
	}
}
