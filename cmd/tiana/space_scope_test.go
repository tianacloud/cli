package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/tianacloud/cli/internal/authclient"
)

func TestInstanceResolutionKeepsOriginalSpace(t *testing.T) {
	for _, product := range []string{"sqlite", "git"} {
		for _, rejected := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/rejected=%t", product, rejected), func(t *testing.T) {
				var client *authclient.Client
				lookups := 0
				client = webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer access" {
						t.Error("lookup changed to another space")
					}
					if r.URL.Path == "/api/v1/instances/shared-name" {
						next, err := client.LoadCredential()
						if err != nil {
							t.Error(err)
							return
						}
						next.User.TenantID, next.AccessToken = "other", "other-access"
						if err := client.SaveCredential(next); err != nil {
							t.Error(err)
						}
						if rejected {
							w.WriteHeader(401)
							io.WriteString(w, `{"error":{"code":"UNAUTHORIZED"}}`)
						} else {
							w.WriteHeader(404)
							io.WriteString(w, `{"error":{"code":"INSTANCE_NOT_FOUND"}}`)
						}
						return
					}
					if r.URL.Path != "/api/v1/instances" {
						t.Errorf("unexpected request %s", r.URL)
						w.WriteHeader(500)
						return
					}
					lookups++
					fmt.Fprintf(w, `{"items":[{"id":"original-instance","engine":%q,"display_name":"shared-name"}],"page":1,"page_size":20,"total":1,"total_pages":1}`, product)
				})
				var instance authclient.Instance
				var err error
				if product == "git" {
					instance, err = resolveGitInstanceWithLogin(t.Context(), client, "shared-name")
				} else {
					instance, err = resolveInstanceWithLogin(t.Context(), client, "shared-name")
				}
				if rejected {
					if err == nil || lookups != 0 {
						t.Fatalf("changed-space authentication retried lookup: %v, %d", err, lookups)
					}
				} else if err != nil || instance.ID != "original-instance" || lookups != 1 {
					t.Fatalf("instance=%+v err=%v lookups=%d", instance, err, lookups)
				}
			})
		}
	}
}

func TestCreateRecoveryRemainsInOriginalSpace(t *testing.T) {
	for _, product := range []string{"sqlite", "git", "function"} {
		t.Run(product, func(t *testing.T) {
			var client *authclient.Client
			writes := 0
			client = webManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/app-types" {
					io.WriteString(w, appTypesResponse(product))
					return
				}
				if r.URL.Path != "/api/v1/instances" || r.Method != "POST" {
					t.Errorf("unexpected %s %s", r.Method, r.URL)
					w.WriteHeader(500)
					return
				}
				writes++
				next, err := client.LoadCredential()
				if err != nil {
					t.Error(err)
					return
				}
				next.User.TenantID, next.AccessToken = "other", "other-access"
				if err := client.SaveCredential(next); err != nil {
					t.Error(err)
				}
				w.WriteHeader(401)
				io.WriteString(w, `{"error":{"code":"UNAUTHORIZED"}}`)
			})
			args := []string{product, "create", "same-name", "--json"}
			var out, diag bytes.Buffer
			if code := runCLI(t.Context(), args, nil, &out, &diag); code != 1 || writes != 1 {
				t.Fatalf("code=%d writes=%d diag=%s", code, writes, &diag)
			}
			path := os.Getenv("TIANA_PENDING_COMMAND_FILE")
			store := authclient.NewFilePendingCommandStore(path)
			pending, err := store.Load()
			if err != nil || pending.TenantID != "tenant" || pending.UserID != "owner" || pending.IdempotencyKey == "" {
				t.Fatalf("pending=%+v err=%v", pending, err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			out.Reset()
			diag.Reset()
			if code := runCLI(t.Context(), args, nil, &out, &diag); code != 1 || writes != 1 {
				t.Fatalf("recovery changed space: code=%d writes=%d diag=%s", code, writes, &diag)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("original recovery record changed")
			}
		})
	}
}
