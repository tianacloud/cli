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
	"sync/atomic"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/testutil/sqlitepeer"
)

// Explicit Token mode must not resolve an instance or open account/local Token stores.
func TestSQLiteDirectEndpointExecute(t *testing.T) {
	t.Setenv("TIANA_TOKEN", sqlitepeer.Token)
	t.Setenv("TIANA_MGR_ORIGIN", "not-an-origin")
	t.Setenv("TIANA_AUTH_ORIGIN", "not-an-origin")
	t.Setenv("TIANA_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "absent", "account"))
	t.Setenv("TIANA_INSTANCE_TOKENS_FILE", filepath.Join(t.TempDir(), "absent", "tokens"))
	for _, endpoint := range []string{sqlitepeer.Endpoint, "https://" + sqlitepeer.Endpoint + ":9443/"} {
		t.Run(endpoint, func(t *testing.T) {
			config, count := sqlitepeer.Gateway(t, func(r io.Reader, w io.Writer) {
				req, err := http.ReadRequest(bufio.NewReader(r))
				if err != nil {
					t.Error(err)
					return
				}
				defer req.Body.Close()
				body, _ := io.ReadAll(req.Body)
				if req.URL.Path != "/v3/pipeline" || !bytes.Contains(body, []byte(`"sql":"SELECT 1"`)) {
					t.Errorf("wrong pipeline: %s", body)
				}
				if req.Header.Get("Authorization") != "" || req.Header.Get("Proxy-Authorization") != "" {
					t.Error("credentials entered SQL request")
				}
				response := `{"baton":null,"results":[{"type":"ok","response":{"type":"execute","result":{"cols":[{"name":"x","decltype":null}],"rows":[[{"type":"integer","value":"1"}]],"affected_row_count":0,"last_insert_rowid":null}}},{"type":"ok","response":{"type":"get_autocommit","is_autocommit":true}},{"type":"ok","response":{"type":"close"}}]}`
				fmt.Fprintf(w, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s", len(response), response)
			}, false)
			resolve := func(context.Context, string, bool) (sqliteResolution, error) {
				t.Error("direct mode called MGR resolver")
				return sqliteResolution{}, io.EOF
			}
			var out, diag bytes.Buffer
			code := runSQLiteWith(context.Background(), []string{"shell", "--endpoint", endpoint, "-e", "SELECT 1", "--format=json"}, strings.NewReader(""), &out, &diag, resolve, &config)
			if code != 0 || diag.Len() != 0 || count.Load() != 1 || !json.Valid(out.Bytes()) {
				t.Fatalf("code=%d connects=%d out=%s diag=%s", code, count.Load(), &out, &diag)
			}
		})
	}
}

func TestSQLiteDirectEndpointRejectsUnsafeArguments(t *testing.T) {
	host := sqlitepeer.Endpoint
	for _, args := range [][]string{
		{"--endpoint", host, "instance"}, {"--endpoint", host, "--branch", "dev"}, {"--endpoint", host, "--branch="},
		{"--endpoint="}, {"--endpoint", host, "--endpoint", host},
		{"--endpoint", "http://" + host}, {"--endpoint", "https://" + host + "/v3/pipeline"},
		{"--endpoint", "https://PRIVATE_ENDPOINT_SECRET@" + host},
		{"--endpoint", "https://" + host + "?PRIVATE_ENDPOINT_SECRET"}, {"--endpoint", "https://" + host + "#PRIVATE_ENDPOINT_SECRET"},
		{"--endpoint", "https://" + host + "?"}, {"--endpoint", "https://" + host + "#"},
		{"--endpoint", "https://" + host + ":"}, {"--endpoint", host + ":0"}, {"--endpoint", host + ":65536"},
		{"--endpoint", host + ":-1"}, {"--endpoint", host + ":https"}, {"--endpoint", " " + host},
		{"--endpoint", "https://[" + host + "]"}, {"--endpoint", "127.0.0.1:443"}, {"--endpoint", "db.example.test"}, {"--endpoint", "ep-01j5c9m7q2v8x4k6n3r0t1w2yz"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out, diag bytes.Buffer
			called := false
			code := runCLIWithSQL(context.Background(), append([]string{"sqlite", "shell"}, args...), strings.NewReader(""), &out, &diag, func(context.Context, sqliteOptions) int { called = true; return 0 })
			if code != 2 || called || strings.Contains(out.String()+diag.String(), "PRIVATE_ENDPOINT_SECRET") {
				t.Fatalf("code=%d called=%v out=%s diag=%s", code, called, &out, &diag)
			}
		})
	}
}

func TestSQLiteDirectEndpointRequiresUsableToken(t *testing.T) {
	for _, value := range []string{"unset", "", "PRIVATE_TOKEN_SECRET\nvalue"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("TIANA_TOKEN", value)
			if value == "unset" {
				if err := os.Unsetenv("TIANA_TOKEN"); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("TIANA_MGR_ORIGIN", "invalid-origin")
			t.Setenv("TIANA_INSTANCE_TOKENS_FILE", filepath.Join(t.TempDir(), "absent"))
			resolve := func(context.Context, string, bool) (sqliteResolution, error) {
				t.Error("missing token attempted MGR lookup")
				return sqliteResolution{}, io.EOF
			}
			var out, diag bytes.Buffer
			code := runSQLiteWith(context.Background(), []string{"shell", "--endpoint", sqlitepeer.Endpoint, "-e", "SELECT 1"}, strings.NewReader(""), &out, &diag, resolve, nil)
			if code != 2 || strings.Contains(diag.String(), "PRIVATE_TOKEN_SECRET\nvalue") {
				t.Fatalf("code=%d diag=%s", code, &diag)
			}
			if (value == "unset" || value == "") && (!strings.Contains(diag.String(), "TIANA_TOKEN")) {
				t.Fatalf("missing token guidance: %s", &diag)
			}
		})
	}
}

func TestSQLiteDirectEndpointTarget(t *testing.T) {
	for _, tc := range []struct{ raw, port string }{
		{sqlitepeer.Endpoint, "443"}, {"https://" + sqlitepeer.Endpoint, "443"},
		{sqlitepeer.Endpoint + ":9443", "9443"}, {"https://" + strings.ToUpper(sqlitepeer.Endpoint) + ":18445/", "18445"},
	} {
		o, err := parseSQLite([]string{"shell", "--endpoint=" + tc.raw})
		if err != nil || o.endpoint != sqlitepeer.Endpoint || o.port != tc.port || o.reference != "" {
			t.Fatalf("raw=%s options=%+v err=%v", tc.raw, o, err)
		}
	}
}

func TestSQLiteDirectEndpointGatewayRefusalAndTLS(t *testing.T) {
	t.Setenv("TIANA_TOKEN", sqlitepeer.Token)
	for _, trusted := range []bool{true, false} {
		config, count := sqlitepeer.Gateway(t, func(io.Reader, io.Writer) { t.Error("SQL after failed CONNECT") }, true)
		if !trusted {
			config.Gateway.RootCAs = nil
		}
		var out, diag bytes.Buffer
		code := runSQLiteWith(context.Background(), []string{"shell", "--endpoint", sqlitepeer.Endpoint, "-e", "SELECT 1"}, strings.NewReader(""), &out, &diag, nil, &config)
		want := "CONNECT_TLS"
		var wantCount int32
		if trusted {
			want = "GATEWAY_REJECTED"
			wantCount = 1
		}
		if code != 3 || count.Load() != wantCount || !strings.Contains(diag.String(), want) {
			t.Fatalf("code=%d connects=%d diag=%s", code, count.Load(), &diag)
		}
	}
}

func TestSQLiteDirectEndpointShellAndScript(t *testing.T) {
	t.Setenv("TIANA_TOKEN", sqlitepeer.Token)
	path := filepath.Join(t.TempDir(), "script.sql")
	if err := os.WriteFile(path, []byte("SELECT 1;\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"shell", "script"} {
		t.Run(mode, func(t *testing.T) {
			var queries atomic.Int32
			config, count := sqlitepeer.Gateway(t, func(r io.Reader, w io.Writer) {
				reader := bufio.NewReader(r)
				for {
					req, err := http.ReadRequest(reader)
					if err == io.EOF {
						return
					}
					if err != nil {
						t.Error(err)
						return
					}
					var body struct {
						Requests []struct {
							Type string `json:"type"`
							Stmt struct {
								SQL string `json:"sql"`
							} `json:"stmt"`
						} `json:"requests"`
					}
					err = json.NewDecoder(req.Body).Decode(&body)
					req.Body.Close()
					if err != nil {
						t.Error(err)
						return
					}
					results := []string{}
					closed := false
					for _, item := range body.Requests {
						switch item.Type {
						case "execute":
							queries.Add(1)
							if item.Stmt.SQL != "SELECT 1;" {
								t.Errorf("SQL changed: %q", item.Stmt.SQL)
							}
							results = append(results, `{"type":"ok","response":{"type":"execute","result":{"cols":[{"name":"x","decltype":null}],"rows":[[{"type":"integer","value":"1"}]],"affected_row_count":0,"last_insert_rowid":null}}}`)
						case "get_autocommit":
							results = append(results, `{"type":"ok","response":{"type":"get_autocommit","is_autocommit":true}}`)
						case "close":
							closed = true
							results = append(results, `{"type":"ok","response":{"type":"close"}}`)
						default:
							t.Errorf("unexpected request %q", item.Type)
							return
						}
					}
					baton := `"next"`
					if closed {
						baton = "null"
					}
					response := `{"baton":` + baton + `,"results":[` + strings.Join(results, ",") + `]}`
					fmt.Fprintf(w, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s", len(response), response)
					if closed {
						return
					}
				}
			}, false)
			args := []string{"shell", "--endpoint", sqlitepeer.Endpoint, "--format=json"}
			input := "SELECT 1;\n.quit\n"
			if mode == "script" {
				args = append(args, "-f", path)
				input = ""
			}
			var out, diag bytes.Buffer
			code := runSQLiteWith(context.Background(), args, strings.NewReader(input), &out, &diag, nil, &config)
			if code != 0 || queries.Load() != 1 || count.Load() != 1 || !json.Valid(out.Bytes()) {
				t.Fatalf("code=%d queries=%d connects=%d out=%s diag=%s", code, queries.Load(), count.Load(), &out, &diag)
			}
		})
	}
}

func TestSQLiteDirectEndpointUsesLocalTokenWithoutMGR(t *testing.T) {
	t.Setenv("TIANA_TOKEN", "")
	if err := os.Unsetenv("TIANA_TOKEN"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TIANA_MGR_ORIGIN", "")
	t.Setenv("TIANA_AUTH_ORIGIN", "")
	path := filepath.Join(t.TempDir(), "tokens.json")
	t.Setenv("TIANA_INSTANCE_TOKENS_FILE", path)
	t.Setenv("TIANA_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "absent-account"))
	_, err := authclient.NewFileInstanceTokenStore(path, "https://mgr.example.test").Save(authclient.InstanceTokenCredential{TenantID: "tenant", InstanceID: "sqlite-one", EndpointID: "ep-01j5c9m7q2v8x4k6n3r0t1w2yz", TokenID: "one", Token: sqlitepeer.Token, ExpiresAt: -1, SavedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	config, count := sqlitepeer.Gateway(t, func(io.Reader, io.Writer) { t.Error("SQL after refusal") }, true)
	resolve := func(context.Context, string, bool) (sqliteResolution, error) {
		t.Error("endpoint-local mode called MGR")
		return sqliteResolution{}, io.EOF
	}
	var out, diag bytes.Buffer
	code := runSQLiteWith(context.Background(), []string{"shell", "--endpoint", sqlitepeer.Endpoint, "-e", "SELECT 1"}, strings.NewReader(""), &out, &diag, resolve, &config)
	if code != 3 || count.Load() != 1 || !strings.Contains(diag.String(), "GATEWAY_REJECTED") {
		t.Fatalf("local Token not used: code=%d connects=%d diag=%s", code, count.Load(), &diag)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("direct connection rewrote Token store")
	}
}

func TestSQLiteDirectEndpointExplicitTokenNeverFallsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	t.Setenv("TIANA_INSTANCE_TOKENS_FILE", path)
	_, err := authclient.NewFileInstanceTokenStore(path, "https://mgr.example.test").Save(authclient.InstanceTokenCredential{TenantID: "tenant", InstanceID: "sqlite-one", EndpointID: strings.SplitN(sqlitepeer.Endpoint, ".", 2)[0], TokenID: "one", Token: sqlitepeer.Token, ExpiresAt: -1})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "PRIVATE_TOKEN_SECRET\nvalue"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("TIANA_TOKEN", value)
			config, count := sqlitepeer.Gateway(t, func(io.Reader, io.Writer) { t.Error("unexpected SQL") }, true)
			var out, diag bytes.Buffer
			code := runSQLiteWith(context.Background(), []string{"shell", "--endpoint", sqlitepeer.Endpoint, "-e", "SELECT 1"}, strings.NewReader(""), &out, &diag, nil, &config)
			if code != 2 || count.Load() != 0 || strings.Contains(diag.String(), "PRIVATE_TOKEN_SECRET\nvalue") {
				t.Fatalf("explicit Token fell back: code=%d connects=%d diag=%s", code, count.Load(), &diag)
			}
		})
	}
}
