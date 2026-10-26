package supervisor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/tianacloud/cli/internal/testutil/account"
	"io"
	"net/http"
	"os"
	"os/exec"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/netconfig"
	"github.com/tianacloud/cli/internal/testutil/proxypeer"
	"strings"
	"testing"
	"time"
)

type compiledBuiltinTestLauncher struct{ path string }

func (l compiledBuiltinTestLauncher) Launch(ctx context.Context, source CredentialSource) (HelperClient, error) {
	command := exec.CommandContext(ctx, l.path, BuiltinHelperArgument)
	command.Env = netconfig.HelperEnvironment(ctx, helperEnvironment(os.Environ(), source))
	if port, ok := ctx.Value(helperNetworkPortKey{}).(string); ok {
		command.Env = append(command.Env, helperNetworkPortEnvironment+"="+port)
	}
	return launchHelperCommand(ctx, command, source, nil)
}

// Optional native acceptance uses a real CLI helper and real Turso against a
// synthetic Gateway/App only. It never addresses a deployed database.
func TestBuiltinActualTurso(t *testing.T) {
	for _, local := range []bool{false, true} {
		name := "explicit"
		if local {
			name = "account"
		}
		t.Run(name, func(t *testing.T) { testBuiltinActualTurso(t, local) })
	}
}

func testBuiltinActualTurso(t *testing.T, local bool) {
	for _, route := range []string{"direct", "http", "socks5h"} {
		t.Run(route, func(t *testing.T) { testBuiltinActualTursoRoute(t, local, route) })
	}
}

func testBuiltinActualTursoRoute(t *testing.T, local bool, route string) {
	cliPath, nativePath := os.Getenv("TIANA_TEST_BUILTIN_CLI"), os.Getenv("TIANA_TEST_NATIVE_TURSO")
	if cliPath == "" || nativePath == "" {
		t.Skip("set TIANA_TEST_BUILTIN_CLI and TIANA_TEST_NATIVE_TURSO for native acceptance")
	}
	t.Setenv("TIANA_TOKEN", builtinTestToken)
	t.Setenv("TURSO_AUTH_TOKEN", "")
	t.Setenv("LIBSQL_AUTH_TOKEN", "")
	cfg, calls := builtinGateway(t, func(w http.ResponseWriter, r *http.Request) {
		acceptBuiltinTunnel(w, r)
		reader := bufio.NewReader(r.Body)
		for {
			req, err := http.ReadRequest(reader)
			if err != nil {
				return
			}
			var body struct {
				Requests []struct {
					Type string `json:"type"`
				} `json:"requests"`
			}
			err = json.NewDecoder(io.LimitReader(req.Body, 1024*1024)).Decode(&body)
			req.Body.Close()
			if err != nil {
				t.Error("invalid Hrana request")
				return
			}
			if req.URL.Path != "/v2/pipeline" && req.URL.Path != "/v3/pipeline" {
				t.Error("unexpected native path")
				return
			}
			if req.Header.Get("Proxy-Authorization") != "" {
				t.Error("outer Token leaked to native Hrana request")
			}
			results := []string{}
			for _, request := range body.Requests {
				switch request.Type {
				case "execute":
					results = append(results, `{"type":"ok","response":{"type":"execute","result":{"cols":[{"name":"1","decltype":null}],"rows":[[{"type":"integer","value":"1"}]],"affected_row_count":0,"last_insert_rowid":null}}}`)
				case "close":
					results = append(results, `{"type":"ok","response":{"type":"close"}}`)
				default:
					t.Errorf("unexpected request type %q", request.Type)
					return
				}
			}
			response := `{"baton":null,"base_url":null,"results":[` + strings.Join(results, ",") + `]}`
			fmt.Fprintf(w, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nContent-Type: application/json\r\n\r\n%s", len(response), response)
			w.(http.Flusher).Flush()
		}
	})
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(name, "")
	}
	if route != "direct" {
		var peer *proxypeer.Proxy
		if route == "socks5h" {
			peer = proxypeer.SOCKS(t, cfg.GatewayAddress, false)
		} else {
			peer = proxypeer.HTTP(t, cfg.GatewayAddress, false)
		}
		t.Setenv("ALL_PROXY", peer.URL)
		cfg.GatewayAddress = cfg.Endpoint + ":443"
		t.Cleanup(func() {
			if len(peer.Requests()) == 0 {
				t.Error("native Turso helper bypassed proxy")
			}
		})
	}
	if local {
		if err := os.Unsetenv("TIANA_TOKEN"); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TIANA_API_ORIGIN", "https://mgr.example.test")
		path := account.CredentialPath(t)
		if err := authclient.NewFileStore(path, "https://mgr.example.test").Save(authclient.Credential{AccessToken: builtinTestToken, RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}

	}
	var output, diag bytes.Buffer
	s := NewSupervisor(compiledBuiltinTestLauncher{cliPath})
	s.IO = IO{Stdin: strings.NewReader(""), Stdout: &output, Stderr: &diag}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code, err := s.Run(ctx, ConnectOptions{NativeArgv: []string{nativePath, "db", "shell", "https://" + cfg.Endpoint, "SELECT 1"}, AdapterID: SQLDAdapterID, Credential: DefaultCredentialSource(), Gateway: GatewayOptions{Address: cfg.GatewayAddress, CACertificates: cfg.RootCertDER}, Security: SecurityPolicy{AllowUnisolated: true}})
	if code != 0 || err != nil || calls.Load() < 1 || !strings.Contains(output.String(), "1") {
		t.Fatalf("code=%d err=%v connects=%d output=%s diagnostics=%s", code, err, calls.Load(), &output, &diag)
	}
}
