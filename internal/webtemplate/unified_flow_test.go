package webtemplate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnifiedCreateRetainsStableWebIdentityAndNullVersion(t *testing.T) {
	calls := 0
	client, _ := fixtureShareClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/api/v1/templates" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if len(body) != 2 || body["source_web_id"] != "source-web" || body["request_id"] != "create-request" {
			t.Errorf("body %v", body)
		}
		w.WriteHeader(201)
		io.WriteString(w, `{"template_id":"stable-template","source_web_id":"source-web","current_version":null,"distribution":{"share_enabled":false,"market_enabled":false}}`)
	})
	for range 2 {
		detail, err := client.CreateTemplate(t.Context(), "source-web", "create-request")
		if err != nil || detail.TemplateID != "stable-template" || detail.SourceWebID != "source-web" || detail.CurrentVersion != nil {
			t.Fatalf("detail=%+v err=%v", detail, err)
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}

func TestUnifiedPublicationKeepsJobVersionSeparateFromCurrent(t *testing.T) {
	uploads, begins, completes, reads := 0, 0, 0, 0
	object := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uploads++
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("credentials reached storage")
		}
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(204)
	}))
	defer object.Close()
	client, _ := fixtureShareClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/templates/stable-template/uploads":
			begins++
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["request_id"] != "publish-request" || body["size"] != float64(8) {
				t.Errorf("body=%v", body)
			}
			json.NewEncoder(w).Encode(map[string]any{"template_id": "stable-template", "job_id": 7, "status": "uploading", "published_version": nil, "form": map[string]any{"url": object.URL, "fields": map[string]string{"key": "fixture/install.zip"}}})
		case "/api/v1/templates/stable-template/uploads/7/complete":
			completes++
			io.WriteString(w, `{"template_id":"stable-template","job_id":7,"status":"pending","published_version":null}`)
		case "/api/v1/templates/stable-template/publications/7":
			reads++
			io.WriteString(w, `{"template_id":"stable-template","job_id":7,"status":"complete","published_version":"9007199254740993","current_version":{"version":"9007199254740994"}}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	file := filepath.Join(t.TempDir(), "install.zip")
	os.WriteFile(file, []byte("reviewed"), 0600)
	receipt, err := client.PublishTemplate(t.Context(), "stable-template", "publish-request", file)
	if err != nil || receipt.JobID != 7 || receipt.Status != "pending" {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	receipt, err = client.WaitTemplatePublication(t.Context(), "stable-template", 7)
	if err != nil || receipt.PublishedVersion == nil || *receipt.PublishedVersion != "9007199254740993" || uploads != 1 || begins != 1 || completes != 1 || reads != 1 {
		t.Fatalf("receipt=%+v err=%v requests=%d/%d/%d/%d", receipt, err, uploads, begins, completes, reads)
	}
	raw, _ := json.Marshal(receipt)
	if strings.Contains(string(raw), "form") {
		t.Fatal("upload authorization leaked")
	}
}

func TestUnifiedWaitRejectsIncompleteUnknownAndForeignReceipts(t *testing.T) {
	for _, tc := range []struct {
		status, version, id string
		job                 int
		wantError           bool
	}{
		{"complete", `"2"`, "stable-template", 7, false},
		{"complete", `null`, "stable-template", 7, true},
		{"fail", `null`, "stable-template", 7, true},
		{"unknown", `"2"`, "stable-template", 7, true},
		{"complete", `"2"`, "foreign-template", 7, true},
		{"complete", `"2"`, "stable-template", 8, true},
	} {
		t.Run(tc.status+tc.id+tc.version, func(t *testing.T) {
			calls := 0
			client, _ := fixtureShareClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				json.NewEncoder(w).Encode(map[string]any{"template_id": tc.id, "job_id": tc.job, "status": tc.status, "published_version": json.RawMessage(tc.version)})
			})
			_, err := client.WaitTemplatePublication(t.Context(), "stable-template", 7)
			if (err != nil) != tc.wantError || calls != 1 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestUnifiedSourceBindsVersionAndPreservesLegacyLink(t *testing.T) {
	data := zipFixture(t, map[string]string{"metadata.json": `{"name":"notes"}`, "app.js": "demo"})
	sum := sha256.Sum256(data)
	for _, mode := range []string{"new link", "fixed market version", "no source", "wrong version", "legacy link"} {
		t.Run(mode, func(t *testing.T) {
			downloads := 0
			object := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				downloads++
				if r.Header.Get("Authorization") != "" {
					t.Error("credential leaked")
				}
				w.Write(data)
			}))
			defer object.Close()
			client, origin := fixtureShareClient(t, func(w http.ResponseWriter, r *http.Request) {
				if mode == "legacy link" {
					if r.URL.Path != "/api/v1/shared-templates/old-web/source" {
						t.Errorf("legacy path %s", r.URL.Path)
					}
				} else {
					if r.URL.Path != "/api/v1/template-catalog/stable-template/source" {
						t.Errorf("path %s", r.URL.Path)
					}
					channel := "share"
					if mode == "fixed market version" || mode == "wrong version" {
						channel = "market"
						if r.URL.Query().Get("version") != "2" {
							t.Error("version lost")
						}
					}
					if r.URL.Query().Get("channel") != channel {
						t.Error("channel lost")
					}
				}
				if mode == "no source" {
					w.WriteHeader(409)
					io.WriteString(w, `{"error":{"code":"TEMPLATE_SOURCE_UNAVAILABLE"}}`)
					return
				}
				version := "2"
				if mode == "wrong version" {
					version = "3"
				}
				json.NewEncoder(w).Encode(map[string]any{"template_id": "stable-template", "source_web_id": "source-web", "version": version, "name": "notes", "url": object.URL + "/source.zip", "size": len(data), "sha256": hex.EncodeToString(sum[:]), "prompt": "make notes"})
			})
			target := filepath.Join(t.TempDir(), "project")
			var result Result
			var err error
			if mode == "fixed market version" || mode == "wrong version" {
				result, err = client.InitUnifiedTemplate(t.Context(), "stable-template", "market", "2", target)
			} else {
				link := origin + "/templates/stable-template"
				if mode == "legacy link" {
					link = origin + "/shared-templates/old-web"
				}
				result, err = client.InitShare(t.Context(), link, target)
			}
			if mode == "no source" || mode == "wrong version" {
				if err == nil || downloads != 0 {
					t.Fatalf("err=%v downloads=%d", err, downloads)
				}
				if _, e := os.Stat(target); !os.IsNotExist(e) {
					t.Fatal("partial target")
				}
				return
			}
			if err != nil || downloads != 1 {
				t.Fatalf("err=%v downloads=%d", err, downloads)
			}
			if mode != "legacy link" && (result.TemplateID != "stable-template" || result.Version != "2") {
				t.Fatalf("identity lost: %+v", result)
			}
		})
	}
}

func TestUnifiedCompleteUnknownPreservesOriginalJobWithoutResubmit(t *testing.T) {
	for _, mode := range []string{"503", "foreign job"} {
		t.Run(mode, func(t *testing.T) {
			begins, completes, uploads := 0, 0, 0
			object := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				uploads++
				io.Copy(io.Discard, r.Body)
				w.WriteHeader(204)
			}))
			defer object.Close()
			client, _ := fixtureShareClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/templates/stable-template/uploads":
					begins++
					json.NewEncoder(w).Encode(map[string]any{"template_id": "stable-template", "job_id": 7, "status": "uploading", "form": map[string]any{"url": object.URL, "fields": map[string]string{"key": "fixture/install.zip"}}})
				case "/api/v1/templates/stable-template/uploads/7/complete":
					completes++
					if mode == "503" {
						w.WriteHeader(503)
						io.WriteString(w, `{"error":{"code":"UNAVAILABLE"}}`)
					} else {
						io.WriteString(w, `{"template_id":"stable-template","job_id":8,"status":"pending","published_version":null}`)
					}
				default:
					t.Errorf("unexpected recovery request %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			file := filepath.Join(t.TempDir(), "install.zip")
			os.WriteFile(file, []byte("reviewed"), 0600)
			receipt, err := client.PublishTemplate(t.Context(), "stable-template", "original-request", file)
			if err == nil || receipt.TemplateID != "stable-template" || receipt.JobID != 7 || uploads != 1 || begins != 1 || completes != 1 {
				t.Fatalf("receipt=%+v error=%v calls=%d/%d/%d", receipt, err, uploads, begins, completes)
			}
		})
	}
}

func TestUnifiedPublicationReplayNeverReuploadsExistingJob(t *testing.T) {
	for _, status := range []string{"pending", "running", "fail", "complete"} {
		t.Run(status, func(t *testing.T) {
			calls := 0
			client, _ := fixtureShareClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.Path != "/api/v1/templates/stable-template/uploads" {
					t.Errorf("replayed job performed extra request: %s %s", r.Method, r.URL.Path)
				}
				var version any
				if status == "complete" {
					version = "2"
				}
				json.NewEncoder(w).Encode(map[string]any{"template_id": "stable-template", "job_id": 7, "status": status, "published_version": version, "last_error": "fixture failure"})
			})
			file := filepath.Join(t.TempDir(), "install.zip")
			os.WriteFile(file, []byte("reviewed"), 0600)
			receipt, err := client.PublishTemplate(t.Context(), "stable-template", "original-request", file)
			if (err != nil) != (status == "fail") || receipt.JobID != 7 || receipt.Status != status || calls != 1 {
				t.Fatalf("receipt=%+v error=%v calls=%d", receipt, err, calls)
			}
		})
	}
}

func TestLegacyPublicationCompleteFailureIsNotSuccess(t *testing.T) {
	object := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body); w.WriteHeader(204) }))
	defer object.Close()
	client, _ := fixtureShareClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/complete") {
			io.WriteString(w, `{"job_id":7,"status":"fail","last_error":"fixture failure"}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"job_id": 7, "status": "uploading", "form": map[string]any{"url": object.URL, "fields": map[string]string{"key": "fixture/install.zip"}}})
	})
	file := filepath.Join(t.TempDir(), "install.zip")
	os.WriteFile(file, []byte("reviewed"), 0600)
	receipt, err := client.PublishShare(t.Context(), "original-web", file)
	if err == nil || receipt.JobID != 7 || receipt.Status != "fail" {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}
