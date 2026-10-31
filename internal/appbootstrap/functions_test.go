package appbootstrap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPreviewFunctionUsesCurrentBindingAndAccount(t *testing.T) {
	s := previewFixture(t, nil)
	calls := []string{}
	allowed := true
	s.sessions["function-preview"] = &session{state: "ready", cancel: func() {}, expires: time.Now().Add(time.Hour), identity: Identity{ID: "owner", FileRequest: func(_ context.Context, method, path string, body, result any) error {
		calls = append(calls, method+" "+path)
		if method == "GET" && path == "/api/v1/web-projects/billing/functions" {
			ids := []string{}
			if allowed {
				ids = append(ids, "fn-api")
			}
			data, _ := json.Marshal(map[string]any{"function_instance_ids": ids})
			return json.Unmarshal(data, result)
		}
		if method != "POST" || path != "/api/v1/functions/fn-api/connection" {
			t.Errorf("unexpected call: %s %s", method, path)
		}
		if value, ok := body.(map[string]string); !ok || value["web_id"] != "billing" {
			t.Fatal("preview must bind the originating Web")
		}
		return json.Unmarshal([]byte(`{"instance_id":"fn-api","origin":"https://function.example.test","token":"function-user-token","expires_at":2000000000}`), result)
	}}}
	call := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://127.0.0.1:4174/web/billing/_tiana/function-connection", strings.NewReader(`{"instance_id":"fn-api"}`))
		r.Header.Set("Origin", "http://127.0.0.1:4174")
		r.Header.Set("X-Tiana-Bootstrap", "1")
		r.AddCookie(&http.Cookie{Name: s.cookie, Value: "function-preview"})
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	w := call()
	if w.Code != 200 || !strings.Contains(w.Body.String(), "function-user-token") || len(calls) != 2 {
		t.Fatal(w.Code, w.Body.String(), calls)
	}
	allowed = false
	w = call()
	if w.Code != 403 || len(calls) != 3 {
		t.Fatal(w.Code, calls)
	}
	sdk := previewRequest(s, "GET", "/web/billing/_tiana/sdk/functions.js", nil)
	if sdk.Code != 200 || !strings.Contains(sdk.Body.String(), "createTianaFunctions") {
		t.Fatal("Function SDK not served")
	}
}

func TestPreviewSessionAppCreator(t *testing.T) {
	for _, owner := range []string{"owner", "another-creator"} {
		s := previewFixture(t, nil)
		s.sessions["creator-preview"] = &session{state: "ready", cancel: func() {}, expires: time.Now().Add(time.Hour), identity: Identity{ID: "owner", TenantID: "tenant", Username: "account-name", DisplayName: "Display Name", FileRequest: func(_ context.Context, method, path string, _, result any) error {
			if method != "GET" || path != "/api/v1/web-projects/billing" {
				t.Fatal("unexpected creator lookup")
			}
			data, _ := json.Marshal(map[string]string{"owner_id": owner})
			return json.Unmarshal(data, result)
		}}}
		response := previewRequest(s, "GET", "/web/billing/_tiana/session", &http.Cookie{Name: s.cookie, Value: "creator-preview"})
		var body struct {
			IsAppOwner *bool  `json:"is_app_owner"`
			Status     string `json:"status"`
			User       struct {
				ID          string `json:"user_id"`
				Username    string `json:"username"`
				DisplayName string `json:"display_name"`
			} `json:"user"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != 200 || body.IsAppOwner == nil || *body.IsAppOwner != (owner == "owner") {
			t.Fatal("wrong preview creator flag")
		}
		if body.Status != "SESSION_ACTIVE" || body.User.ID != "owner" || body.User.Username != "account-name" || body.User.DisplayName != "Display Name" {
			t.Fatalf("preview session user: %+v", body)
		}
	}
}
