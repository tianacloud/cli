package updatecheck

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/tianacloud/cli/internal/clientconfig"
	"github.com/tianacloud/cli/internal/netconfig"
	"github.com/tianacloud/cli/internal/testutil/proxypeer"
)

func TestBothPackagesThroughAllProxy(t *testing.T) {
	var calls atomic.Int32
	server, roots := proxypeer.TLS(t, "registry.example.test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Error("credential reached registry")
		}
		io.WriteString(w, `{"version":"2.0.0"}`)
	}))
	peer := proxypeer.SOCKS(t, server.Listener.Addr().String(), false)
	ctx := netconfig.WithEnvironment(context.Background(), []string{"ALL_PROXY=" + peer.URL})
	ctx = clientconfig.WithTrust(ctx, &clientconfig.Trust{Roots: roots})
	result := (Checker{Path: filepath.Join(t.TempDir(), "update-state.json"), Registry: "https://registry.example.test"}).Check(ctx, map[string]string{CLIPackage: "1.0.0", SkillsPackage: "1.0.0"}, Options{Fetch: true})
	if calls.Load() != 2 || len(result.Updates) != 2 {
		t.Fatalf("checks: calls=%d result=%+v", calls.Load(), result)
	}
	for _, r := range peer.Requests() {
		if r.Address != "registry.example.test:443" {
			t.Fatal("registry did not use proxy")
		}
	}
}
