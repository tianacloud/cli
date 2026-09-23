package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/testutil/sqlitepeer"
)

// Exercise the same urfave command/flag declarations without running SQL.
func parseSQLite(args []string) (sqliteOptions, error) {
	var options sqliteOptions
	parsed := false
	var diagnostics bytes.Buffer
	code := runCLIWithSQL(context.Background(), append([]string{"sqlite"}, args...), strings.NewReader(""), io.Discard, &diagnostics,
		func(_ context.Context, value sqliteOptions) int { options = value; parsed = true; return 0 })
	if code != 0 || !parsed {
		return options, fmt.Errorf("invalid SQLite arguments: %s", &diagnostics)
	}
	return options, nil
}

func TestSQLiteParseAndEndpoint(t *testing.T) {
	for _, args := range [][]string{{"shell", "db", "--execute", "BEGIN", "--token", "SECRET"}, {"shell", "db", "-f", "x", "--atomic"}, {"shell", "db", "--url", "http://x"}, {"shell", "db", "-e"}, {"shell", "db", "--timeout", "0"}, {"shell", "db", "--format", "xml"}, {"shell", "db", "--token-env", "A", "--token-file", "B"}, {"shell", "db", "--output", "a", "--output", "b"}} {
		if _, err := parseSQLite(args); err == nil {
			t.Errorf("accepted %v", args)
		} else if strings.Contains(err.Error(), "SECRET") {
			t.Fatal("echoed secret")
		}
	}
	if _, err := parseSQLite([]string{"shell", "db", "--execute=SELECT 1", "--format=json"}); err != nil {
		t.Fatal(err)
	}
	i := authclient.Instance{Engine: "sqlite", EndpointID: "ep-01j5c9m7q2v8x4k6n3r0t1w2yz", ObservedState: "sleeping", Connection: &authclient.InstanceConnection{Hostname: "ep-01j5c9m7q2v8x4k6n3r0t1w2yz.db.example.test"}}
	if _, _, err := sqliteEndpoint(i); err != nil {
		t.Fatal(err)
	}
	i.Connection.URL = "https://" + i.Connection.Hostname + ":9443"
	if endpoint, port, err := sqliteEndpoint(i); err != nil || port != "9443" || endpoint != "ep-01j5c9m7q2v8x4k6n3r0t1w2yz.db.example.test" {
		t.Fatalf("connection URL target = %q:%q, err=%v", endpoint, port, err)
	}
	i.Connection.Hostname = i.EndpointID + ".db.dev.tiana.test"
	i.Connection.URL = "https://" + i.Connection.Hostname + ":18445"
	if endpoint, port, err := sqliteEndpoint(i); err != nil || port != "18445" || endpoint != i.Connection.Hostname {
		t.Fatalf("deployment URL target = %q:%q, err=%v", endpoint, port, err)
	}
	i.EndpointID = "ep-00000000000000000000000000"
	if _, _, err := sqliteEndpoint(i); err == nil {
		t.Fatal("accepted mismatched endpoint identity")
	}
	i.Engine = "mysql"
	if _, _, err := sqliteEndpoint(i); err == nil {
		t.Fatal("accepted wrong engine")
	}
}

func TestSQLitePreflightBeforeResolve(t *testing.T) {
	resolve := func(context.Context, string, bool) (sqliteResolution, error) {
		t.Error("preflight performed network lookup")
		return sqliteResolution{}, io.EOF
	}
	for _, sql := range []string{"SELECT 1; SELECT FROM;", "BEGIN", "SELECT 1; SELECT 2"} {
		var out, errout bytes.Buffer
		status := runSQLiteWith(context.Background(), []string{"shell", "db", "--execute", sql}, strings.NewReader(""), &out, &errout, resolve, nil)
		if status != 2 {
			t.Fatalf("status %d %s", status, errout.String())
		}
	}
	path := filepath.Join(t.TempDir(), "output")
	if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{path, path + ".link"} {
		if target != path {
			if err := os.Symlink(path, target); err != nil {
				t.Fatal(err)
			}
		}
		var out, errout bytes.Buffer
		status := runSQLiteWith(context.Background(), []string{"shell", "db", "--execute", "SELECT 1", "--output", target}, strings.NewReader(""), &out, &errout, resolve, nil)
		if status != 6 {
			t.Fatal(status, errout.String())
		}
	}
	data, _ := os.ReadFile(path)
	if string(data) != "preserve" {
		t.Fatal("overwrote output")
	}
}

func TestSQLiteCommandExecWithoutHelper(t *testing.T) {
	t.Setenv("TIANA_TOKEN", "tia_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	// No helper path or install is configured; this exercises the native branch.
	resolve := func(_ context.Context, ref string, nonInteractive bool) (sqliteResolution, error) {
		if ref != "db" || !nonInteractive {
			t.Error("wrong resolution arguments")
		}
		return sqliteResolution{instance: authclient.Instance{Engine: "sqlite", Connection: &authclient.InstanceConnection{Hostname: "ep-01j5c9m7q2v8x4k6n3r0t1w2yz.db.example.test"}}}, nil
	}
	config, count := sqlitepeer.Gateway(t, func(r io.Reader, w io.Writer) {
		req, err := http.ReadRequest(bufio.NewReader(r))
		if err != nil {
			t.Error(err)
			return
		}
		defer req.Body.Close()
		body, _ := io.ReadAll(req.Body)
		var input struct{ Requests []struct{ Type string } }
		if req.URL.Path != "/v3/pipeline" || json.Unmarshal(body, &input) != nil || len(input.Requests) != 3 || input.Requests[2].Type != "close" {
			t.Error("wrong exec pipeline")
		}
		response := `{"baton":null,"results":[{"type":"ok","response":{"type":"execute","result":{"cols":[{"name":"x","decltype":null}],"rows":[[{"type":"integer","value":"1"}]],"affected_row_count":0,"last_insert_rowid":null}}},{"type":"ok","response":{"type":"get_autocommit","is_autocommit":true}},{"type":"ok","response":{"type":"close"}}]}`
		fmt.Fprintf(w, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s", len(response), response)
	}, false)
	var out, errout bytes.Buffer
	status := runSQLiteWith(context.Background(), []string{"shell", "db", "--execute", "SELECT 1", "--format", "json"}, strings.NewReader(""), &out, &errout, resolve, &config)
	if status != 0 || errout.Len() != 0 || count.Load() != 1 || !json.Valid(out.Bytes()) {
		t.Fatalf("status=%d err=%s out=%s", status, errout.String(), out.String())
	}
}
