//go:build darwin || linux

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/tianacloud/cli/internal/authclient"
	"golang.org/x/sys/unix"
)

// A terminal must not select an interactive pager or consume a queued quit key.
func TestProductListTerminalFetchesAllRecordsWithoutMore(t *testing.T) {
	for _, product := range []string{"sqlite", "git", "web"} {
		t.Run(product, func(t *testing.T) {
			var requested []string
			webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/instances" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				page := r.URL.Query().Get("page")
				requested = append(requested, page)
				n := 1
				if page == "2" {
					n = 2
				}
				item := authclient.Instance{ID: fmt.Sprintf("inst-%d", n), DisplayName: fmt.Sprintf("record-%d", n), Engine: product, ProductRevision: "1", CreatedAt: "2026-10-05T00:00:00Z"}
				if product == "web" {
					item.Web = &authclient.WebInstanceMetadata{ID: fmt.Sprintf("web-%d", n), Endpoint: fmt.Sprintf("https://site.example.test/web/web-%d/", n)}
				}
				if err := json.NewEncoder(w).Encode(authclient.InstancePage{Items: []authclient.Instance{item}, Page: n, PageSize: 20, Total: 2, TotalPages: 2}); err != nil {
					t.Error(err)
				}
			})
			master, slave := openDeleteTestPTY(t)
			defer master.Close()
			defer slave.Close()
			if _, err := master.Write([]byte("q\n")); err != nil {
				t.Fatal(err)
			}
			var diagnostics bytes.Buffer
			code := runCLI(t.Context(), []string{product, "list"}, slave, slave, &diagnostics)
			if err := unix.SetNonblock(int(slave.Fd()), true); err != nil {
				t.Fatal(err)
			}
			queued := make([]byte, 16)
			n, err := unix.Read(int(slave.Fd()), queued)
			if err != nil || string(queued[:max(n, 0)]) != "q\n" {
				t.Fatalf("list consumed terminal input: n=%d err=%v", n, err)
			}
			if err := unix.SetNonblock(int(master.Fd()), true); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			buf := make([]byte, 4096)
			for {
				n, err := unix.Read(int(master.Fd()), buf)
				if n > 0 {
					output.Write(buf[:n])
				}
				if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if code != 0 || strings.Join(requested, ",") != "1,2" || !strings.Contains(output.String(), "record-1") || !strings.Contains(output.String(), "record-2") || strings.Contains(output.String(), "-- More --") || strings.Count(output.String(), "ID ") != 1 {
				t.Fatalf("code=%d pages=%v stdout=%q stderr=%q", code, requested, output.String(), diagnostics.String())
			}
		})
	}
}
