//go:build linux || darwin

package sqlitecli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/testutil/proxypeer"
	tianasqlite "github.com/tianacloud/sdk-go-sqlite"
	"github.com/tianacloud/sdk-go/outbound"
)

// Opt-in black-box test against the actual app_sqlite library in a test-only
// peer executable. Relative sockets avoid sun_path limits on long workspaces.
func realAppConfig(t *testing.T, ttl time.Duration) tianasqlite.Config {
	t.Helper()
	binary := os.Getenv("TIANA_SQLITE_APP_PEER_BINARY")
	if binary == "" {
		t.Skip("set TIANA_SQLITE_APP_PEER_BINARY; see scripts/test-sqlite-app.sh")
	}
	dir := t.TempDir()
	t.Chdir(dir)
	log, err := os.Create(filepath.Join(dir, "app.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), fmt.Sprintf("TIANA_TEST_STREAM_TTL_MS=%d", ttl.Milliseconds()))
	cmd.Dir = dir
	cmd.Stdout = log
	cmd.Stderr = log
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	socket := "s"
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err = os.Stat(filepath.Join(dir, "s")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(filepath.Join(dir, "app.log"))
			t.Fatalf("App socket not ready: %s", data)
		}
		time.Sleep(10 * time.Millisecond)
	}
	dial, _ := nativeGateway(t, func(r io.Reader, w io.Writer) {
		conn, e := net.Dial("unix", socket)
		if e != nil {
			t.Error(e)
			return
		}
		defer conn.Close()
		copyDone := make(chan struct{})
		go func() { defer close(copyDone); _, _ = io.Copy(conn, r); _ = conn.Close() }()
		_, _ = io.Copy(w, conn)
		_ = conn.Close()
		<-copyDone
	}, false)
	return dial
}

func TestRealAppOverNativeSDK(t *testing.T) {
	dial := realAppConfig(t, time.Minute)
	newClient := func() *Client { c := NewClient(dial, 2*time.Second); t.Cleanup(c.Close); return c }
	c := newClient()
	queries := []string{
		"CREATE TABLE t(x INTEGER, s TEXT)",
		"CREATE TRIGGER tr AFTER INSERT ON t BEGIN UPDATE t SET s='trigger;é' WHERE x=new.x; END;",
		"INSERT INTO t VALUES(9223372036854775807, 'before')",
		"SELECT x, s, x'00ff', NULL, '' FROM t",
	}
	for _, sql := range queries {
		parts, e := Split(sql)
		if e != nil {
			t.Fatal(sql, e)
		}
		r, e := c.Execute(context.Background(), parts[0].SQL, false)
		if e != nil {
			t.Fatal(e)
		}
		if strings.HasPrefix(sql, "SELECT") {
			if len(r.Rows) != 1 || displayValue(r.Rows[0][0]) != "9223372036854775807" || displayValue(r.Rows[0][1]) != "trigger;é" || displayValue(r.Rows[0][2]) != "base64:AP8" {
				t.Fatalf("wrong typed result: %+v", r)
			}
		}
	}
	if e := c.Finish(context.Background(), nil); e != nil {
		t.Fatal(e)
	}
	c = newClient()
	for _, sql := range []string{"BEGIN IMMEDIATE", "SAVEPOINT nested", "INSERT INTO t VALUES(3,'savepoint')", "ROLLBACK TO nested", "RELEASE nested", "COMMIT", "BEGIN EXCLUSIVE", "INSERT INTO t VALUES(2,'uncommitted')"} {
		if _, e := c.Execute(context.Background(), sql, false); e != nil {
			t.Fatal(e)
		}
	}
	if e := c.Finish(context.Background(), nil); e == nil || e.Code != "UNCOMMITTED_TRANSACTION" {
		t.Fatal(e)
	}
	c = newClient()
	r, e := c.Execute(context.Background(), "SELECT count(*) FROM t", true)
	if e != nil {
		t.Fatal(e)
	}
	if displayValue(r.Rows[0][0]) != "1" {
		t.Fatal("uncommitted row survived cleanup")
	}
}

func TestRealAppInteractiveExpiry(t *testing.T) {
	config := realAppConfig(t, 100*time.Millisecond)
	c := NewClient(config, 2*time.Second)
	defer c.Close()
	var diagnostics bytes.Buffer
	execute := func(q string) *Result {
		t.Helper()
		r, e := c.executeInteractive(context.Background(), q, &diagnostics)
		if e != nil {
			t.Fatal(q, e)
		}
		return r
	}
	execute("CREATE TABLE expiry_test(id INTEGER)")
	time.Sleep(150 * time.Millisecond)
	execute("INSERT INTO expiry_test VALUES(1)")
	if r := execute("SELECT count(*) FROM expiry_test"); displayValue(r.Rows[0][0]) != "1" {
		t.Fatal("wrong write count", r.Rows)
	}
	execute("BEGIN")
	execute("INSERT INTO expiry_test VALUES(2)")
	time.Sleep(150 * time.Millisecond)
	_, e := c.executeInteractive(context.Background(), "INSERT INTO expiry_test VALUES(3)", &diagnostics)
	if e == nil || e.Code != "BATON_INVALID" || e.Outcome == "unknown" {
		t.Fatalf("expired transaction: %v", e)
	}
	if r := execute("SELECT count(*) FROM expiry_test"); displayValue(r.Rows[0][0]) != "1" {
		t.Fatal("expired transaction persisted", r.Rows)
	}
	if !strings.Contains(diagnostics.String(), "Reconnecting...") {
		t.Fatal("missing recovery progress")
	}
	if e := c.Finish(context.Background(), nil); e != nil {
		t.Fatal(e)
	}
}

func TestRealAppParseErrorTransactionThroughProxy(t *testing.T) {
	for _, route := range []string{"direct", "http", "socks5h"} {
		t.Run(route, func(t *testing.T) {
			config := realAppConfig(t, time.Minute)
			if route != "direct" {
				var peer *proxypeer.Proxy
				if route == "http" {
					peer = proxypeer.HTTP(t, config.Gateway.DialAddress, false)
				} else {
					peer = proxypeer.SOCKS(t, config.Gateway.DialAddress, false)
				}
				policy, err := outbound.New(outbound.Config{AllProxy: peer.URL})
				if err != nil {
					t.Fatal(err)
				}
				config.Gateway.Network = policy
				config.Gateway.DialAddress = net.JoinHostPort(config.Gateway.Endpoint, "443")
				t.Cleanup(func() {
					if len(peer.Requests()) == 0 {
						t.Error("real App traffic did not traverse the proxy")
					}
				})
			}
			client := NewClient(config, 2*time.Second)
			t.Cleanup(client.Close)
			execute := func(sql string) *Result {
				t.Helper()
				result, err := client.Execute(context.Background(), sql, false)
				if err != nil {
					t.Fatal(sql, err)
				}
				return result
			}
			execute("CREATE TABLE continuity(x INTEGER)")
			execute("CREATE TEMP TABLE same_session(x INTEGER)")
			execute("INSERT INTO same_session VALUES(7)")
			for _, finish := range []string{"ROLLBACK", "COMMIT"} {
				execute("BEGIN")
				execute("INSERT INTO continuity VALUES(1)")
				_, err := client.Execute(context.Background(), "SELEC 1", false)
				if err == nil || err.Code != "SQL_PARSE_ERROR" || err.Outcome == "unknown" {
					t.Fatalf("expected known SQL parse error: %v", err)
				}
				if auto, known := client.Autocommit(); !known || auto {
					t.Fatal("parse error lost the active transaction")
				}
				if result := execute("SELECT x FROM same_session"); displayValue(result.Rows[0][0]) != "7" {
					t.Fatal("parse error lost the original session")
				}
				execute("INSERT INTO continuity VALUES(2)")
				execute(finish)
				if finish == "ROLLBACK" {
					if result := execute("SELECT count(*) FROM continuity"); displayValue(result.Rows[0][0]) != "0" {
						t.Fatal("parse error broke explicit rollback")
					}
				}
			}
			if err := client.Finish(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			reader := NewClient(config, 2*time.Second)
			defer reader.Close()
			result, failure := reader.Execute(context.Background(), "SELECT sum(x), count(*) FROM continuity", true)
			if failure != nil {
				t.Fatal(failure)
			}
			if displayValue(result.Rows[0][0]) != "3" || displayValue(result.Rows[0][1]) != "2" {
				t.Fatal("committed transaction contents changed", result.Rows)
			}
		})
	}
}
