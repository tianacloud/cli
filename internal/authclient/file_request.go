package authclient

import (
	"context"
	"crypto/tls"
	"net/http"

	"github.com/tianacloud/sdk-go/auth"
)

// RequestFileJSON sends with the account snapshot already checked by the caller.
func (c *Client) RequestFileJSON(ctx context.Context, method, path string, body any, credential Credential, result any) error {
	cfg := c.config
	cfg.Store = &pinnedAppCredential{value: credential}
	client, err := auth.NewWithConfig(cfg)
	if err != nil {
		return err
	}
	_, err = client.DoJSON(ctx, method, path, body, nil, result)
	return err
}

// FileHTTPClient keeps deployment trust while separating object traffic from auth.
func (c *Client) FileHTTPClient() *http.Client {
	client := http.Client{}
	if c.config.HTTPClient != nil {
		client = *c.config.HTTPClient
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if current, ok := client.Transport.(*http.Transport); ok {
		transport = current.Clone()
	}
	if c.config.RootCAs != nil {
		transport.TLSClientConfig = &tls.Config{RootCAs: c.config.RootCAs, MinVersion: tls.VersionTLS12}
	}
	client.Transport = transport
	client.Jar = nil
	return &client
}
