package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tianacloud/cli/internal/authclient"
)

func TestCreateRejectsRemovedFlagsBeforeRequests(t *testing.T) {
	for _, product := range []string{"sqlite", "git"} {
		engine := "sqlite"
		if product == "git" {
			engine = "git"
		}
		for _, args := range [][]string{
			{"--display-name", "name"}, {"--display-name=name"}, {"--display", "name"},
			{"name", "--engine", engine}, {"name", "--engine=" + engine},
			{"--engine", engine, "name"},
		} {
			t.Run(product+strings.Join(args, "/"), func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(503) }))
				defer server.Close()
				env := newTestEnv(t, server.URL)
				pending := []byte(`{"command":"db.create","origin":"https://previous.example.test","args":["create","pending"],"idempotency_key":"keep-original"}`)
				if err := os.WriteFile(env.pendingPath, pending, 0600); err != nil {
					t.Fatal(err)
				}
				var out, diag bytes.Buffer
				code := runCLI(context.Background(), append([]string{product, "create"}, args...), strings.NewReader(""), &out, &diag)
				after, err := os.ReadFile(env.pendingPath)
				if code != 2 || calls.Load() != 0 || out.Len() != 0 || err != nil || !bytes.Equal(pending, after) {
					t.Fatalf("code=%d calls=%d unchanged=%t error=%v", code, calls.Load(), bytes.Equal(pending, after), err)
				}
			})
		}
		t.Run(product+"/help", func(t *testing.T) {
			var out, diag bytes.Buffer
			code := runCLI(context.Background(), []string{product, "create", "--help"}, strings.NewReader(""), &out, &diag)
			if code != 0 || !strings.Contains(out.String(), "NAME") || strings.Contains(out.String(), "--display-name") || strings.Contains(out.String(), "--engine") {
				t.Fatalf("unexpected create help: code=%d %s %s", code, &out, &diag)
			}
		})
	}
}

func TestCreatePositionalNameAndFixedEngine(t *testing.T) {
	for _, product := range []string{"sqlite", "git"} {
		engine := "sqlite"
		if product == "git" {
			engine = "git"
		}
		for _, name := range []string{"project", " spaced name ", "--engine"} {
			t.Run(product+"/"+name, func(t *testing.T) {
				creates := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/api/v1/app-types":
						io.WriteString(w, appTypesResponse("sqlite", "git"))
					case "/api/v1/instances":
						creates++
						var got authclient.CreateInstanceRequest
						if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got.DisplayName != name || got.Engine != engine {
							t.Errorf("wrong create payload: %+v error=%v", got, err)
						}
						if product == "git" {
							io.WriteString(w, gitInstanceResponse())
						} else {
							io.WriteString(w, sqliteInstanceResponse())
						}
					case "/api/v1/instances/" + testInstanceID:
						if product == "git" {
							io.WriteString(w, gitInstanceResponse())
						} else {
							io.WriteString(w, sqliteInstanceResponse())
						}
					default:
						t.Errorf("unexpected request: %s", r.URL.Path)
						w.WriteHeader(404)
					}
				}))
				defer server.Close()
				env := newTestEnv(t, server.URL)
				saveTestCredential(t, server.URL, env.credentialsPath, "usr_create")
				args := []string{product, "create", name}
				if name != "project" {
					args = []string{product, "create", "--", name}
				}
				var out, diag bytes.Buffer
				code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diag)
				if code != 0 || creates != 1 {
					t.Fatalf("code=%d creates=%d diagnostics=%s", code, creates, &diag)
				}
			})
		}
	}
}
