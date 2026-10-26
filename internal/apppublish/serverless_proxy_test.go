package apppublish

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/tianacloud/cli/internal/netconfig"
	"github.com/tianacloud/sdk-go/fetch"
)

func TestServerlessProxyBypassUsesLogicalPortWithPhysicalOverride(t *testing.T) {
	const endpoint = "ep-00000000000000000000000000.web.example.test"
	const physical = "physical.example.test:10443"
	t.Setenv("TIANA_GATEWAY_ADDRESS", physical)
	t.Setenv("TIANA_TOKEN", "tia_3"+strings.Repeat("A", 43))
	t.Setenv("TIANA_TOKEN_FILE", "")
	if err := os.Unsetenv("TIANA_TOKEN_FILE"); err != nil {
		t.Fatal(err)
	}
	for _, scheme := range []string{"http", "socks5h"} {
		for _, test := range []struct {
			name      string
			bypass    string
			wantProxy bool
		}{
			{name: "logical_port", bypass: endpoint + ":9443"},
			{name: "default_port", bypass: endpoint + ":443", wantProxy: true},
			{name: "physical_port", bypass: endpoint + ":10443", wantProxy: true},
		} {
			t.Run(scheme+"/"+test.name, func(t *testing.T) {
				proxy := scheme + "://proxy.example.test:1080"
				ctx := netconfig.WithEnvironment(t.Context(), []string{"ALL_PROXY=" + proxy, "NO_PROXY=" + test.bypass})
				runner, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, request *http.Request) {
					switch request.URL.Path {
					case "/api/v1/web-projects/web-one":
						io.WriteString(w, `{"id":"web-one","instance_id":"web-instance","owner_id":"prn-test","tenant_id":"ten-test"}`)
					case "/api/v1/instances/web-instance":
						io.WriteString(w, `{"id":"web-instance","engine":"web","product_state":"ACTIVE","connection":{"hostname":"`+endpoint+`","url":"https://`+endpoint+`:9443"}}`)
					default:
						t.Errorf("unexpected management request %s", request.URL.Path)
						w.WriteHeader(http.StatusNotFound)
					}
				})
				transport := &fakeWebFetch{t: t}
				factories := 0
				runner.Fetch = func(config fetch.Config) (FetchTransport, error) {
					factories++
					if config.DialAddress != physical || config.Endpoint != endpoint || config.Network == nil {
						t.Fatal("physical override, logical endpoint or network policy was not preserved")
					}
					// Match the SDK's target URL, including its default logical port.
					port := config.Port
					if port == "" {
						port = "443"
					}
					selected, err := config.Network.ProxyForURL(&url.URL{Scheme: "https", Host: net.JoinHostPort(config.Endpoint, port)})
					if err != nil {
						t.Fatal(err)
					}
					if (selected != nil) != test.wantProxy {
						t.Errorf("NO_PROXY=%s selected proxy=%v, want proxy=%t (logical port=%s)", test.bypass, selected, test.wantProxy, port)
					}
					if selected != nil && selected.String() != proxy {
						t.Errorf("selected proxy=%s, want ALL_PROXY=%s", selected, proxy)
					}
					return transport, nil
				}
				result := runner.Run(ctx, Options{Command: "status", ID: "web-one"})
				if result.Error != nil || factories != 1 || transport.calls != 1 {
					t.Fatalf("status failed or was replayed: result=%+v factories=%d requests=%d", result, factories, transport.calls)
				}
			})
		}
	}
}
