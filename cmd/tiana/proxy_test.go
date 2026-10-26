package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tianacloud/cli/internal/testutil/proxypeer"
	"github.com/tianacloud/cli/internal/testutil/sqlitepeer"
)

func TestSQLiteCommandThroughAllProxy(t *testing.T) {
	for _, kind := range []string{"http", "socks5h"} {
		t.Run(kind, func(t *testing.T) {
			var cert []byte
			config, calls := sqlitepeer.Gateway(t, func(r io.Reader, w io.Writer) {
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
			} else {
				peer = proxypeer.SOCKS(t, config.Gateway.DialAddress, false)
			}
			for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy", "TIANA_TOKEN_FILE"} {
				t.Setenv(name, "")
			}
			if err := os.Unsetenv("TIANA_TOKEN_FILE"); err != nil {
				t.Fatal(err)
			}
			t.Setenv("ALL_PROXY", peer.URL)
			t.Setenv("TIANA_TOKEN", sqlitepeer.Token)
			ca := filepath.Join(t.TempDir(), "ca.pem")
			if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}), 0600); err != nil {
				t.Fatal(err)
			}
			var output, diagnostics bytes.Buffer
			code := runCLI(context.Background(), []string{"--ca-file", ca, "sqlite", "shell", "--endpoint", sqlitepeer.Endpoint, "-e", "SELECT 1", "--format", "json"}, strings.NewReader(""), &output, &diagnostics)
			if code != 0 || calls.Load() != 1 || !strings.Contains(output.String(), `"1"`) {
				t.Fatalf("code=%d output=%s error=%s", code, &output, &diagnostics)
			}
			requests := peer.Requests()
			if len(requests) != 1 || requests[0].Address != sqlitepeer.Endpoint+":443" {
				t.Fatalf("proxy route: %+v", requests)
			}
		})
	}
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
