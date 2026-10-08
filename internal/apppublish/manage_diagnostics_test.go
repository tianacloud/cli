package apppublish

import (
	"fmt"
	"net/http"
	"testing"
)

func TestWebSemanticFailuresRetainResponseRequestID(t *testing.T) {
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
				_, e = r.Deletion(t.Context(), "AAAAAAAAAAAA", &WebProjectDeleteRequest{})
			case "deletion":
				_, e = r.Deletion(t.Context(), "AAAAAAAAAAAA", nil)
			}
			if e == nil || requestID == "" || e.RequestID != requestID {
				t.Fatalf("mode=%s sent=%q error=%+v", mode, requestID, e)
			}
		})
	}
}

func TestWebNameOutcomesRetainLastPageRequestID(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(fmt.Sprint(ambiguous), func(t *testing.T) {
			var requestID string
			r, _, _, _ := runnerForTest(t, func(w http.ResponseWriter, req *http.Request) {
				requestID = req.Header.Get("X-Request-ID")
				if ambiguous {
					writeWebInstancePage(t, w, 1, 1, []WebProject{{ID: "web-a", Name: "same name"}, {ID: "web-b", Name: "same name"}})
				} else {
					writeWebInstancePage(t, w, 1, 0, []WebProject{})
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
