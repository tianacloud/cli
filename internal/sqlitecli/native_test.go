package sqlitecli

import (
	"bufio"
	"context"
	"github.com/tianacloud/cli/internal/testutil/sqlitepeer"
	tianasqlite "github.com/tianacloud/sdk-go-sqlite"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const nativeEndpoint = sqlitepeer.Endpoint

func nativeGateway(t *testing.T, inner func(io.Reader, io.Writer), refuse bool) (tianasqlite.Config, *atomic.Int32) {
	return sqlitepeer.Gateway(t, inner, refuse)
}
func TestNativeSDKHranaAndRefusal(t *testing.T) {
	for _, refuse := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "refusal"}[refuse], func(t *testing.T) {
			var sqlBytes atomic.Int32
			dial, count := nativeGateway(t, func(r io.Reader, w io.Writer) {
				reader := bufio.NewReader(r)
				req, e := readPeer(reader)
				if e != nil {
					t.Error(e)
					return
				}
				sqlBytes.Add(1)
				io.WriteString(w, httpReply(successResponse(req, "")))
			}, refuse)
			c := NewClient(dial, time.Second)
			defer c.Close()
			_, e := c.Execute(context.Background(), "SELECT 1", true)
			if refuse {
				if e == nil || e.ExitCode != 3 || sqlBytes.Load() != 0 {
					t.Fatalf("refused: %v bytes=%d", e, sqlBytes.Load())
				}
			} else if e != nil {
				t.Fatal(e)
			}
			if count.Load() != 1 {
				t.Fatal("unexpected CONNECT count")
			}
		})
	}
}

func TestNativeSDKSessionSurvivesRequestContext(t *testing.T) {
	dial, count := nativeGateway(t, func(r io.Reader, w io.Writer) {
		reader := bufio.NewReader(r)
		for i := 0; ; i++ {
			req, e := readPeer(reader)
			if e != nil {
				return
			}
			body := successResponse(req, "next")
			if req.Requests[0].Statement != nil && strings.EqualFold(req.Requests[0].Statement.SQL, "ROLLBACK") {
				body = rollbackResponse("next")
			}
			if _, e = io.WriteString(w, httpReply(body)); e != nil {
				return
			}
		}
	}, false)
	c := NewClient(dial, time.Second)
	defer c.Close()
	for range 3 {
		if _, e := c.Execute(context.Background(), "SELECT 1", false); e != nil {
			t.Fatal(e)
		}
	}
	if e := c.Finish(context.Background(), nil); e != nil {
		t.Fatal(e)
	}
	if count.Load() != 1 {
		t.Fatal("unexpected reconnect")
	}
}
