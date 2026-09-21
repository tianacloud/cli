//go:build linux || darwin

package sqlitecli

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Opt-in black-box test against the actual app_sqlite library in a test-only
// peer executable. Relative sockets avoid sun_path limits on long workspaces.
func TestRealAppOverNativeSDK(t *testing.T) {
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
