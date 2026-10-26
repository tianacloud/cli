package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Exercise the real serve command's client injection, launch authorization and
// server-side file proxy. The browser-facing requests stay on loopback.
func previewFileThroughCLI(t *testing.T, ca, build string) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	output, writer := io.Pipe()
	var diagnostics bytes.Buffer
	done := make(chan int, 1)
	go func() {
		code := runCLI(ctx, []string{"--ca-file", ca, "web", "serve", "a1b2c3d4e5f6", "--dir", build, "--port", strconv.Itoa(port), "--json"}, strings.NewReader(""), writer, &diagnostics)
		writer.Close()
		done <- code
	}()
	defer func() {
		cancel()
		output.Close()
		select {
		case code := <-done:
			if code != 0 {
				t.Errorf("preview exited %d: %s", code, &diagnostics)
			}
		case <-time.After(3 * time.Second):
			t.Error("preview did not stop after cancellation")
		}
	}()
	var startup struct {
		Data struct {
			LaunchURL string `json:"launch_url"`
		} `json:"data"`
	}
	if err := json.NewDecoder(output).Decode(&startup); err != nil {
		t.Fatal("preview startup", err)
	}
	u, err := url.Parse(startup.Data.LaunchURL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || !strings.HasPrefix(u.Fragment, "tiana_launch=") {
		t.Fatal("preview did not use saved account authorization")
	}
	origin := u.Scheme + "://" + u.Host
	client := &http.Client{Transport: &http.Transport{}, Timeout: 2 * time.Second}
	defer client.CloseIdleConnections()
	request := func(method, path string, cookie *http.Cookie) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, origin+"/web/a1b2c3d4e5f6/_tiana/"+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", origin)
		req.Header.Set("X-Tiana-Bootstrap", "1")
		if path == "local-login" {
			req.Header.Set("X-Tiana-Launch", strings.TrimPrefix(u.Fragment, "tiana_launch="))
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	login := request("POST", "local-login", nil)
	_, _ = io.Copy(io.Discard, login.Body)
	login.Body.Close()
	if login.StatusCode != 200 || len(login.Cookies()) != 1 {
		t.Fatal("preview local login failed", login.StatusCode)
	}
	content := request("GET", "file-storage/content?path=hello.txt", login.Cookies()[0])
	data, err := io.ReadAll(content.Body)
	content.Body.Close()
	if err != nil || content.StatusCode != 200 || string(data) != "hello" {
		t.Fatalf("preview content status=%d bytes=%q err=%v", content.StatusCode, data, err)
	}
}
