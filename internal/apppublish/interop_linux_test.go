//go:build linux

package apppublish

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/x509"
	"encoding/json"
	"errors"
	tiana "github.com/tianacloud/sdk-go"
	"github.com/tianacloud/sdk-go/fetch"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Opt-in local integration: actual Gateway TLS/framing, gnet Agent, app_web and
// S3. Control/Runtime route setup are fixtures, separately tested by their repos.
func TestExternalServerlessWebFetch(t *testing.T) {
	path := os.Getenv("TIANA_WEB_INTEROP_GATEWAY")
	if path == "" {
		t.Skip("requires isolated Gateway/Agent/app_web/S3")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Port        int    `json:"port"`
		Hostname    string `json:"hostname"`
		Certificate []int  `json:"certificate_der"`
		DonePort    int    `json:"done_port"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		t.Fatal("gateway configuration")
	}
	der := make([]byte, len(cfg.Certificate))
	for i, v := range cfg.Certificate {
		der[i] = byte(v)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	client, err := fetch.NewClient(fetch.Config{Endpoint: cfg.Hostname, DialAddress: net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Port)), RootCAs: roots, Token: "tia_1AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	resources, err := fetch.NewResourceClient(fetch.Config{Endpoint: cfg.Hostname, DialAddress: net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Port)), RootCAs: roots})
	if err != nil {
		t.Fatal(err)
	}
	defer resources.Close()
	anonymous, err := fetch.NewClient(fetch.Config{Endpoint: cfg.Hostname, DialAddress: net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Port)), RootCAs: roots})
	if err != nil {
		t.Fatal(err)
	}
	defer anonymous.Close()
	for _, path := range []string{"/_tiana/web/current", "/_tiana/web/publish/unknown"} {
		_, denied := anonymous.Do(t.Context(), fetch.Request{Method: "GET", PathQuery: path})
		var policy *fetch.Error
		if !errors.As(denied, &policy) || (policy.Status != 401 && policy.Status != 403) {
			t.Fatalf("anonymous control should be denied: %v", denied)
		}
	}
	dir := t.TempDir()
	content := []byte(strings.Repeat("personal web payload\n", 1000))
	if err = os.WriteFile(filepath.Join(dir, "file.txt"), content, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "plain.bin"), []byte{0, 1, 2, 3, 4}, 0600); err != nil {
		t.Fatal(err)
	}
	archive, sha, err := PackFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { archive.Close(); os.Remove(archive.Name()) }()
	info, _ := archive.Stat()
	id := "0123456789abcdef0123456789abcdef"
	response, err := client.Do(t.Context(), fetch.Request{Method: "PUT", PathQuery: "/_tiana/web/publish", Body: archive, BodyLength: info.Size(), Headers: []fetch.Header{{Name: "x-tiana-publish-id", Value: id}, {Name: "x-tiana-content-sha256", Value: sha}}})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.Status != 200 {
		t.Fatalf("publish %d %s %v", response.Status, body, err)
	}
	var publication struct {
		Uploaded bool   `json:"uploaded"`
		SHA      string `json:"sha256"`
		ID       string `json:"publish_id"`
	}
	if json.Unmarshal(body, &publication) != nil || !publication.Uploaded || publication.SHA != sha || publication.ID != id {
		t.Fatal("upload receipt")
	}
	activated := false
	for i := 0; i < 100; i++ {
		r, e := client.Do(t.Context(), fetch.Request{Method: "GET", PathQuery: "/_tiana/web/current"})
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(r.Body)
		r.Body.Close()
		var current struct {
			Serving string `json:"serving_sha256"`
			Remote  string `json:"remote_sha256"`
		}
		if e == nil && json.Unmarshal(b, &current) == nil && current.Serving == sha && current.Remote == sha {
			activated = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !activated {
		t.Fatal("activation did not follow upload")
	}
	var encodedLength, etag string
	for _, method := range []string{"GET", "HEAD"} {
		r, e := resources.Do(t.Context(), fetch.Request{Method: method, PathQuery: "/file.txt", Headers: []fetch.Header{{Name: "accept-encoding", Value: "gzip"}}})
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(r.Body)
		r.Body.Close()
		if e != nil || r.Status != 200 || r.Header.Get("Content-Encoding") != "gzip" {
			t.Fatalf("encoded %s %d %v", method, r.Status, e)
		}
		if method == "GET" {
			encodedLength = r.Header.Get("Content-Length")
			etag = r.Header.Get("ETag")
			decoder, e := gzip.NewReader(bytes.NewReader(b))
			if e != nil {
				t.Fatal(e)
			}
			original, e := io.ReadAll(decoder)
			decoder.Close()
			if e != nil || !bytes.Equal(original, content) {
				t.Fatal("raw Deflate gzip CRC/content")
			}
		} else if len(b) != 0 || r.Header.Get("Content-Length") != encodedLength || r.Header.Get("ETag") != etag {
			t.Fatal("HEAD representation")
		}
	}
	for _, tc := range []struct {
		method, path string
		headers      []fetch.Header
		status       int
		body         []byte
	}{{"GET", "/file.txt", []fetch.Header{{Name: "accept-encoding", Value: "identity"}}, 200, content}, {"GET", "/file.txt", []fetch.Header{{Name: "accept-encoding", Value: "gzip"}, {Name: "if-none-match", Value: etag}}, 304, nil}, {"GET", "/plain.bin", []fetch.Header{{Name: "range", Value: "bytes=1-3"}}, 206, []byte{1, 2, 3}}, {"GET", "/", nil, 404, nil}, {"GET", "/data/file.txt", nil, 404, nil}, {"GET", "/web.yaml", nil, 404, nil}, {"GET", "/_tiana/web/current", nil, 404, nil}} {
		r, e := resources.Do(t.Context(), fetch.Request{Method: tc.method, PathQuery: tc.path, Headers: tc.headers})
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(r.Body)
		r.Body.Close()
		if e != nil || r.Status != tc.status || tc.body != nil && !bytes.Equal(b, tc.body) {
			t.Fatalf("%s %s status %d want %d err %v", tc.method, tc.path, r.Status, tc.status, e)
		}
	}
	native, err := tiana.NewClient(tiana.Config{Endpoint: cfg.Hostname, DialAddress: net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Port)), RootCAs: roots})
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		stream, err := native.Connect(t.Context(), tiana.TianaHTTP)
		if err != nil {
			t.Fatal(err)
		}
		request, _ := http.NewRequest(method, "http://app.invalid/_tiana/web/publish", nil)
		if err = request.Write(stream); err != nil {
			t.Fatal(err)
		}
		reply, err := http.ReadResponse(bufio.NewReader(stream), request)
		if err != nil {
			t.Fatal(err)
		}
		if reply.StatusCode != 405 || reply.Header.Get("Allow") != "GET, HEAD" {
			t.Fatalf("raw %s bypassed resource-only socket: %d", method, reply.StatusCode)
		}
		reply.Body.Close()
		stream.Close()
	}
	controlResource, err := client.Do(t.Context(), fetch.Request{Method: "GET", PathQuery: "/file.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if controlResource.Status != 404 {
		t.Fatal("HTTP control socket served resources")
	}
	controlResource.Body.Close()
	t.Logf("real Gateway/Agent/App/S3: archive=%d, original=%d, gzip=%s, upload+reload/GET/HEAD/304/Range/path isolation verified", info.Size(), len(content), encodedLength)
	if conn, e := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.DonePort))); e == nil {
		conn.Close()
	} else {
		t.Fatal(e)
	}
}
