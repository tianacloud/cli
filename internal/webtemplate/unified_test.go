package webtemplate

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestUnifiedStatusPreservesStableIdentityAndVersion(t *testing.T) {
	client, _ := fixtureShareClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/templates/template-one" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		io.WriteString(w, `{"template_id":"template-one","source_web_id":"original-web","source_web_deleted":true,"distribution":{"share_enabled":true,"market_enabled":false},"current_version":{"version":"18446744073709551615","has_source":false,"installable":true}}`)
	})
	result, err := client.TemplateStatus(t.Context(), "template-one")
	if err != nil || result.TemplateID != "template-one" || result.SourceWebID != "original-web" || !result.SourceWebDeleted || result.CurrentVersion == nil || result.CurrentVersion.Version != "18446744073709551615" || result.CurrentVersion.HasSource || !result.CurrentVersion.Installable {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func TestUnifiedDistributionReturnsFixedReceipt(t *testing.T) {
	for _, field := range []string{"share_enabled", "market_enabled"} {
		t.Run(field, func(t *testing.T) {
			calls := 0
			client, _ := fixtureShareClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "PUT" || r.URL.Path != "/api/v1/templates/template-one/distribution" {
					t.Errorf("request %s %s", r.Method, r.URL.Path)
				}
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if len(body) != 3 || body[field] != false || body["request_id"] != "fixed-request" || body["expected_revision"] != "9007199254740993" {
					t.Errorf("body=%v", body)
				}
				after := map[string]bool{"share_enabled": true, "market_enabled": true}
				after[field] = false
				json.NewEncoder(w).Encode(map[string]any{"template_id": "template-one", "request_id": "fixed-request", "before": map[string]any{"revision": "9007199254740993", "distribution": map[string]bool{"share_enabled": true, "market_enabled": true}}, "after": map[string]any{"revision": "9007199254740994", "distribution": after}, "updated_at": 123})
			})
			disabled := false
			update := DistributionUpdate{RequestID: "fixed-request", ExpectedRevision: "9007199254740993"}
			if field == "share_enabled" {
				update.ShareEnabled = &disabled
			} else {
				update.MarketEnabled = &disabled
			}
			result, err := client.SetTemplateDistribution(t.Context(), "template-one", update)
			if err != nil || calls != 1 || result.After.Revision != "9007199254740994" {
				t.Fatalf("result=%+v err=%v calls=%d", result, err, calls)
			}
		})
	}
}

func TestUnifiedErrorsNeverFallBackToLegacyWrites(t *testing.T) {
	for _, code := range []int{404, 409, 503} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			calls := 0
			client, _ := fixtureShareClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "PUT" || r.URL.Path != "/api/v1/templates/template-one/distribution" {
					t.Errorf("unexpected fallback %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(code)
				io.WriteString(w, `{"error":{"code":"TEMPLATE_INSTALLATION_MAINTENANCE","message":"unavailable"}}`)
			})
			enabled := true
			_, err := client.SetTemplateDistribution(t.Context(), "template-one", DistributionUpdate{RequestID: "request", ExpectedRevision: "1", ShareEnabled: &enabled})
			if err == nil || calls != 1 {
				t.Fatalf("error=%v calls=%d", err, calls)
			}
		})
	}
}

func TestUnifiedRejectsForeignReceiptAndEmptyUpdates(t *testing.T) {
	calls := 0
	client, _ := fixtureShareClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.WriteString(w, `{"template_id":"another-template"}`)
	})
	if _, err := client.TemplateStatus(t.Context(), "template-one"); err == nil {
		t.Fatal("foreign receipt accepted")
	}
	if _, err := client.SetTemplateDistribution(t.Context(), "template-one", DistributionUpdate{}); err == nil {
		t.Fatal("empty update accepted")
	}
	if calls != 1 {
		t.Fatal("empty update accessed server")
	}
}
