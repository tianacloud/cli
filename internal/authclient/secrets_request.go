package authclient

import (
	"context"

	"github.com/tianacloud/sdk-go/auth"
)

// RequestSecretsJSON uses the selected management account; values are never logged.
func (c *Client) RequestSecretsJSON(ctx context.Context, method, path string, body any, credential Credential, result any) error {
	cfg := c.config
	cfg.Store = &pinnedAppCredential{value: credential}
	client, err := auth.NewWithConfig(cfg)
	if err != nil {
		return err
	}
	_, err = client.DoJSON(ctx, method, path, body, nil, result)
	return err
}
