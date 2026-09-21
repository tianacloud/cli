package supervisor

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
	"time"
)

// Supply a native helper built from the matching SDK to exercise the real
// language boundary and embedded launch path without sending a credential.
func TestEmbeddedHelperTLSConfig(t *testing.T) {
	path := os.Getenv("TIANA_TEST_HELPER")
	if path == "" {
		t.Skip("set TIANA_TEST_HELPER to a native SDK helper")
	}
	image, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(image)
	launcher, err := NewEmbeddedHelperLauncher(image, fmt.Sprintf("%x", digest))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	helper, err := launcher.Launch(ctx, DefaultCredentialSource())
	if err != nil {
		t.Fatal("launch", err)
	}
	defer helper.Close()
	if _, err = helper.Handshake(ctx, []uint16{HelperContractVersion}, "tls-integration"); err != nil {
		t.Fatal("handshake", err)
	}
	c := HelperConfig{Endpoint: testEndpoint(t).Hostname(), AdapterID: SQLDAdapterID, AllowedProfile: []string{ProfileHranaHTTP, ProfileHranaWebSocket}, SelectionMode: SelectionModeBoundedHTTPHeaderClassifier, Deadline: SQLDClassifierDeadline, HeaderLimit: SQLDHeaderLimit, FieldLimit: SQLDFieldLimit, Pre200Limit: SQLDPre200Limit, UseWebPKIRoots: !debugTLSAvailable, InsecureTLS: debugTLSAvailable}
	if err = helper.Configure(ctx, c); err != nil {
		t.Fatal("configure", err)
	}
	if err = helper.DeliverCredential(ctx, nil); err != nil {
		t.Fatal("credential", err)
	}
	if _, err = helper.WaitBound(ctx); err != nil {
		t.Fatal("bound", err)
	}
	if _, err = helper.WaitReady(ctx); err != nil {
		t.Fatal("ready", err)
	}
}
