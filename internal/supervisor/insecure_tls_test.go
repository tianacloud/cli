package supervisor

import (
	"testing"

	"github.com/tianacloud/cli/internal/buildconfig"
)

func TestBuildInsecureTLSPolicyDefaultsToVerify(t *testing.T) {
	previous := buildconfig.InsecureTLS
	buildconfig.InsecureTLS = "false"
	defer func() { buildconfig.InsecureTLS = previous }()

	trust, err := resolveGatewayTrust(GatewayOptions{})
	if err != nil || !trust.UseWebPKIRoots || trust.InsecureTLS {
		t.Fatalf("default trust=%+v err=%v", trust, err)
	}
}

func TestBuildInsecureTLSDisablesGatewayVerification(t *testing.T) {
	previous := buildconfig.InsecureTLS
	buildconfig.InsecureTLS = "true"
	defer func() { buildconfig.InsecureTLS = previous }()

	trust, err := resolveGatewayTrust(GatewayOptions{})
	if err != nil || trust.UseWebPKIRoots || !trust.InsecureTLS {
		t.Fatalf("insecure trust=%+v err=%v", trust, err)
	}
}
