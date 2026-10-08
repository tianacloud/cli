package gitremote

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tianacloud/cli/internal/clientconfig"
	"github.com/tianacloud/cli/internal/supervisor"
)

func TestGitConnectReportsPreNetworkFailureStage(t *testing.T) {
	endpoint, err := supervisor.ParseEndpoint("ep-0" + strings.Repeat("a", 25) + ".db.example.test")
	if err != nil {
		t.Fatal(err)
	}
	ctx := clientconfig.WithTrust(context.Background(), &clientconfig.Trust{})
	t.Setenv("TIANA_TOKEN", "tia_"+strings.Repeat("A", 44))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedPort := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name    string
		address string
		want    string
	}{
		{"dns", "nonexistent-gateway.invalid:443", "Gateway DNS resolution failed"},
		{"tcp_refused", net.JoinHostPort("127.0.0.1", strconv.Itoa(closedPort)), "Gateway TCP connection refused"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Setenv("TIANA_GATEWAY_ADDRESS", scenario.address)
			_, err := connect(ctx, endpoint)
			if err == nil || !strings.Contains(err.Error(), scenario.want) || !strings.Contains(err.Error(), "request ID req-") {
				t.Fatalf("Git connection diagnostic=%v, want %q and request ID", err, scenario.want)
			}
		})
	}
}

func TestClosedConnectEnvelope(t *testing.T) {
	valid := func() *http.Response {
		return &http.Response{StatusCode: 200, ProtoMajor: 2, Header: http.Header{"Tiana-Tunnel-Version": []string{"1"}, "Tiana-Request-Id": []string{"req-test"}, "Tiana-Auth-Mode": []string{"DISABLED"}}}
	}
	if !validSuccess(valid(), "req-test") {
		t.Fatal("valid envelope rejected")
	}
	for _, mutate := range []func(*http.Response){
		func(r *http.Response) { r.ProtoMajor = 1 },
		func(r *http.Response) { r.StatusCode = 204 },
		func(r *http.Response) { r.Header.Set("Unexpected", "value") },
		func(r *http.Response) { r.Header.Add("Tiana-Request-Id", "req-test") },
		func(r *http.Response) { r.Header.Set("Tiana-Request-Id", "req-other") },
		func(r *http.Response) { r.Header.Del("Tiana-Tunnel-Version") },
		func(r *http.Response) { r.Header.Set("Tiana-Auth-Mode", "unknown") },
	} {
		r := valid()
		mutate(r)
		if validSuccess(r, "req-test") {
			t.Fatal("invalid success envelope accepted")
		}
	}
	r := valid()
	r.Header.Set("Tiana-Auth-Mode", "TOKEN_REQUIRED")
	if !validSuccess(r, "req-test") {
		t.Fatal("authenticated envelope rejected")
	}
}
