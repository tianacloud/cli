package apppublish

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/tianacloud/sdk-go/fetch"
)

func TestPublishByNameLoadsDeliveryAddressesFromResolvedID(t *testing.T) {
	details := 0
	r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, q *http.Request) {
		switch q.URL.Path {
		case "/api/v1/web-projects/abcdefghijkl":
			details++
			io.WriteString(w, `{"id":"abcdefghijkl","endpoint":"https://site.example.test/web/abcdefghijkl/","console_url":"https://console.example.test/console/web/abcdefghijkl","mini_program_url":"https://console.example.test/mini-program/apps/abcdefghijkl","mini_program_code_url":"https://console.example.test/api/v1/mini-program/apps/abcdefghijkl/code","instance_id":"web-instance","owner_id":"prn-test","tenant_id":"ten-test"}`)
		case "/api/v1/instances/web-instance":
			io.WriteString(w, `{"id":"web-instance","engine":"web","product_state":"ACTIVE","connection":{"hostname":"ep-01j5c9m7q2v8x4k6n3r0t1w2yz.example.test","url":"https://ep-01j5c9m7q2v8x4k6n3r0t1w2yz.example.test"}}`)
		case "/api/v1/instances":
			writeWebInstancePage(t, w, 1, 1, []WebProject{{ID: "abcdefghijkl", Name: "my-app", OwnerID: "prn-test", TenantID: "ten-test", InstanceID: "web-instance"}})
		default:
			w.WriteHeader(404)
		}
	})
	fake := &fakeWebFetch{t: t}
	r.Fetch = func(fetch.Config) (FetchTransport, error) { return fake, nil }
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("site"), 0600); err != nil {
		t.Fatal(err)
	}
	result := r.Run(t.Context(), Options{Command: "publish", ID: "my-app", Dir: dir})
	if result.Error != nil || details != 1 {
		t.Fatalf("publish %+v, detail reads=%d", result, details)
	}
	data := result.Data.(map[string]any)
	for _, field := range []string{"endpoint", "console_url", "mini_program_url", "mini_program_code_url"} {
		if value, _ := data[field].(string); value == "" {
			t.Fatalf("missing %s: %+v", field, data)
		}
	}
}
