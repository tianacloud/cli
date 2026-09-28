package apppublish

import (
	"fmt"
	"net/http"
	"testing"
)

func TestAppSemanticFailuresRetainResponseRequestID(t *testing.T) {
	for _, mode := range []string{"create", "list", "resolve", "delete", "deletion"} {
		t.Run(mode, func(t *testing.T) {
			var requestID string
			r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) {
				requestID = req.Header.Get("X-Request-ID")
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{}`))
			})
			r, e := r.Bind(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "create":
				e = r.Run(t.Context(), Options{Command: "create", Name: "Fixture", RequestID: "fixture-stable"}).Error
			case "list":
				_, e = r.ListPage(t.Context(), "")
			case "resolve":
				_, e = r.Resolve(t.Context(), "AAAAAAAAAAAA")
			case "delete":
				_, e = r.Deletion(t.Context(), "AAAAAAAAAAAA", &AppDeleteRequest{})
			case "deletion":
				_, e = r.Deletion(t.Context(), "AAAAAAAAAAAA", nil)
			}
			if e == nil || requestID == "" || e.RequestID != requestID {
				t.Fatalf("mode=%s sent=%q error=%+v", mode, requestID, e)
			}
		})
	}
}

func TestAppNameOutcomesRetainLastPageRequestID(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(fmt.Sprint(ambiguous), func(t *testing.T) {
			var requestID string
			r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) {
				requestID = req.Header.Get("X-Request-ID")
				if ambiguous {
					w.Write([]byte(`{"items":[{"app_id":"apps-a","name":"same name","owner_id":"prn-test","tenant_id":"ten-test"},{"app_id":"apps-b","name":"same name","owner_id":"prn-test","tenant_id":"ten-test"}]}`))
				} else {
					w.Write([]byte(`{"items":[]}`))
				}
			})
			r, e := r.Bind(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			_, e = r.Resolve(t.Context(), "same name")
			if e == nil || requestID == "" || e.RequestID != requestID {
				t.Fatalf("last page=%q error=%+v", requestID, e)
			}
		})
	}
}
