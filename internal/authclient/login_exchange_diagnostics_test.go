package authclient

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type exchangeDiagnosticTransport func(*http.Request) (*http.Response, error)

func (f exchangeDiagnosticTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAuthorizationExchangeRequestIdentity(t *testing.T) {
	for _, mode := range []string{"success", "rejected", "invalid-json", "network", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			observed := ""
			sent := ""
			transport := exchangeDiagnosticTransport(func(r *http.Request) (*http.Response, error) {
				sent = r.Header.Get("X-Request-ID")
				if sent == "" || observed != sent {
					t.Errorf("identity missing before transport: observed=%q sent=%q", observed, sent)
				}
				if mode == "network" {
					return nil, io.ErrUnexpectedEOF
				}
				if mode == "cancelled" {
					return nil, context.Canceled
				}
				status := 200
				body := `{"access_token":"access","refresh_token":"refresh","expires_in":3600,"user":{"user_id":"owner"}}`
				if mode == "rejected" {
					status = 503
				}
				if mode == "invalid-json" {
					body = "{"
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			c, err := NewWithConfig(Config{Origin: "http://localhost:8080", Store: NewFileStore(t.TempDir()+"/credentials.json", "http://localhost:8080"), HTTPClient: &http.Client{Transport: transport}, OnRequestID: func(id string) { observed = id }})
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.exchangeAuthorizationCode(t.Context(), AuthTransaction{ID: "transaction", ClientSecret: "secret"}, "code")
			if mode == "success" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || sent == "" || !strings.Contains(err.Error(), sent) {
				t.Fatalf("error lost request ID: %v", err)
			}
		})
	}
}
