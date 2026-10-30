package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

func TestAuthOnlyReadsLocalSessionWithoutMutations(t *testing.T) {
	for _, mode := range []string{"missing", "valid", "expired", "no-expiry", "other-origin", "malformed", "directory"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("auth-only made a network request: %s", r.URL.Path)
				w.WriteHeader(503)
			}))
			defer server.Close()
			env := newTestEnv(t, server.URL)
			root := os.Getenv("XDG_CONFIG_HOME")
			if mode == "directory" {
				if err := os.MkdirAll(env.credentialsPath, 0700); err != nil {
					t.Fatal(err)
				}
			} else if mode != "missing" {
				origin := server.URL
				if mode == "other-origin" {
					origin = "https://other.example.test"
				}
				expires := time.Now().Add(time.Hour)
				if mode == "expired" {
					expires = time.Now().Add(-time.Hour)
				}
				if mode == "no-expiry" {
					expires = time.Time{}
				}
				if err := authclient.NewFileStore(env.credentialsPath, origin).Save(authclient.Credential{
					AccessToken: "synthetic-access", RefreshToken: "synthetic-refresh", ExpiresAt: expires,
				}); err != nil {
					t.Fatal(err)
				}
				if mode == "malformed" {
					if err := os.WriteFile(env.credentialsPath, []byte("invalid synthetic-secret"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			// Capture entries, modes, and file content/mtime, including absence of lock/update files.
			// Directory timestamps can settle after fixture creation on Windows.
			snapshot := func() map[string]string {
				files := map[string]string{}
				err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					info, err := d.Info()
					if err != nil {
						return err
					}
					files[path] = info.Mode().String()
					if !d.IsDir() {
						b, err := os.ReadFile(path)
						if err != nil {
							return err
						}
						files[path] += info.ModTime().String() + string(b)
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				return files
			}
			before := snapshot()
			for _, jsonMode := range []bool{true, false} {
				args := []string{"status", "--auth-only"}
				if jsonMode {
					args = append(args, "--json")
				}
				var out, diag bytes.Buffer
				code := runCLI(t.Context(), args, nil, &out, &diag)
				wantCode := 0
				if mode == "malformed" || mode == "directory" {
					wantCode = 1
				}
				if code != wantCode {
					t.Fatalf("code=%d want=%d: %s %s", code, wantCode, &out, &diag)
				}
				if jsonMode {
					var result struct {
						Status string
						Data   map[string]any
						Error  *struct{ Code string }
					}
					if err := json.Unmarshal(out.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					if wantCode == 0 {
						if result.Status != "succeeded" || result.Data["logged_in"] != (mode == "valid") || result.Error != nil {
							t.Fatalf("unexpected result: %s", &out)
						}
					} else if result.Status != "failed" || result.Error == nil || result.Error.Code != "LOCAL_STATE_UNAVAILABLE" || result.Data != nil {
						t.Fatalf("local failure presented as login state: %s", &out)
					}
				}
				if strings.Contains(out.String()+diag.String(), "synthetic-") {
					t.Fatal("credential content disclosed")
				}
				after := snapshot()
				if !reflect.DeepEqual(before, after) {
					for path, previous := range before {
						if after[path] != previous {
							t.Errorf("local state changed: %s", path)
						}
					}
					t.Fatal("auth-only mutated local state")
				}
			}
			if passiveUpdateAllowed([]string{"status", "--auth-only"}) {
				t.Fatal("auth-only can start update worker")
			}
		})
	}
}
