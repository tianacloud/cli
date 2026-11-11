package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/testutil/proxypeer"
	"github.com/tianacloud/cli/internal/testutil/sqlitepeer"
	"golang.org/x/net/dns/dnsmessage"
)

func TestSQLiteCommandThroughAllProxy(t *testing.T) {
	for _, kind := range []string{"http", "socks5h", "compat"} {
		t.Run(kind, func(t *testing.T) {
			var cert []byte
			makeGateway := sqlitepeer.Gateway
			if kind == "compat" {
				makeGateway = sqlitepeer.CompatGateway
			}
			config, calls := makeGateway(t, func(r io.Reader, w io.Writer) {
				request, err := http.ReadRequest(bufio.NewReader(r))
				if err != nil {
					t.Error(err)
					return
				}
				defer request.Body.Close()
				body, _ := io.ReadAll(request.Body)
				if !bytes.Contains(body, []byte(`"sql":"SELECT 1"`)) || request.Header.Get("Proxy-Authorization") != "" {
					t.Error("SQL or credential boundary")
				}
				response := `{"baton":null,"results":[{"type":"ok","response":{"type":"execute","result":{"cols":[{"name":"x","decltype":null}],"rows":[[{"type":"integer","value":"1"}]],"affected_row_count":0,"last_insert_rowid":null}}},{"type":"ok","response":{"type":"get_autocommit","is_autocommit":true}},{"type":"ok","response":{"type":"close"}}]}`
				fmt.Fprintf(w, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s", len(response), response)
			}, false, &cert)
			var peer *proxypeer.Proxy
			if kind == "http" {
				peer = proxypeer.HTTP(t, config.Gateway.DialAddress, false)
			} else if kind != "compat" {
				peer = proxypeer.SOCKS(t, config.Gateway.DialAddress, false)
			}
			for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy", "TIANA_TOKEN_FILE"} {
				t.Setenv(name, "")
			}
			if err := os.Unsetenv("TIANA_TOKEN_FILE"); err != nil {
				t.Fatal(err)
			}
			if peer != nil {
				t.Setenv("ALL_PROXY", peer.URL)
			} else {
				t.Setenv("ALL_PROXY", "http://127.0.0.1:1")
				t.Setenv("TIANA_GATEWAY_ADDRESS", config.Gateway.DialAddress)
			}
			t.Setenv("TIANA_TOKEN", sqlitepeer.Token)
			ca := filepath.Join(t.TempDir(), "ca.pem")
			if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}), 0600); err != nil {
				t.Fatal(err)
			}
			var output, diagnostics bytes.Buffer
			args := []string{"--ca-file", ca, "sqlite", "shell", "--endpoint", sqlitepeer.Endpoint, "-e", "SELECT 1", "--format", "json"}
			if kind == "compat" {
				resolveFixtureEndpoint(t)
				_, port, _ := net.SplitHostPort(config.Gateway.DialAddress)
				args[5] = "https://" + sqlitepeer.Endpoint + ":" + port + "/"
				args = append([]string{"--transport", "compat"}, args...)
			}
			code := runCLI(context.Background(), args, strings.NewReader(""), &output, &diagnostics)
			if code != 0 || calls.Load() != 1 || !strings.Contains(output.String(), `"1"`) {
				t.Fatalf("code=%d output=%s error=%s", code, &output, &diagnostics)
			}
			if peer == nil {
				return
			}
			requests := peer.Requests()
			if len(requests) != 1 || requests[0].Address != sqlitepeer.Endpoint+":443" {
				t.Fatalf("proxy route: %+v", requests)
			}
		})
	}
}

// Resolve only this test Endpoint on loopback, keeping production URL parsing,
// physical port selection and the SDK's actual direct dial in the exercised path.
func resolveFixtureEndpoint(t *testing.T) {
	previous := net.DefaultResolver
	t.Cleanup(func() { net.DefaultResolver = previous })
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			server.SetDeadline(time.Now().Add(time.Second))
			var size [2]byte
			if _, err := io.ReadFull(server, size[:]); err != nil {
				return
			}
			wire := make([]byte, binary.BigEndian.Uint16(size[:]))
			if _, err := io.ReadFull(server, wire); err != nil {
				return
			}
			var request dnsmessage.Message
			if err := request.Unpack(wire); err != nil {
				return
			}
			reply := dnsmessage.Message{Header: dnsmessage.Header{ID: request.ID, Response: true, RecursionAvailable: true}, Questions: request.Questions}
			for _, q := range request.Questions {
				if q.Name.String() != sqlitepeer.Endpoint+"." {
					t.Error("unexpected DNS target")
					return
				}
				if q.Type == dnsmessage.TypeA {
					reply.Answers = append(reply.Answers, dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: q.Type, Class: dnsmessage.ClassINET}, Body: &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}})
				}
			}
			wire, err := reply.Pack()
			if err != nil {
				return
			}
			binary.BigEndian.PutUint16(size[:], uint16(len(wire)))
			server.Write(append(size[:], wire...))
		}()
		return client, nil
	}}
}

func TestProxyInvalidConfigurationKeepsLocalCommandsUsable(t *testing.T) {
	t.Setenv("ALL_PROXY", "bad://user:secret@proxy.example.test")
	for _, args := range [][]string{{"version"}, {"--help"}, {"sqlite", "shell", "--help"}} {
		var out, errout bytes.Buffer
		if code := runCLI(context.Background(), args, strings.NewReader(""), &out, &errout); code != 0 {
			t.Fatalf("local command blocked: %v %s", args, &errout)
		}
	}
	var out, errout bytes.Buffer
	if code := runCLI(context.Background(), []string{"sqlite", "list"}, strings.NewReader(""), &out, &errout); code == 0 || !strings.Contains(errout.String(), "ALL_PROXY") || strings.Contains(errout.String(), "secret") {
		t.Fatalf("proxy configuration result: %d %s", code, &errout)
	}
}
