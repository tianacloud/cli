package authclient

import (
	"context"

	"github.com/tianacloud/sdk-go/auth"
)

// RequestFunctionJSON retains the account selected before resolving a function.
func (c *Client) RequestFunctionJSON(ctx context.Context, method, path string, body any, credential Credential, result any) error {
	cfg := c.config
	cfg.Store = &pinnedAppCredential{value: credential}
	client, err := auth.NewWithConfig(cfg)
	if err != nil {
		return err
	}
	_, err = client.DoJSON(ctx, method, path, body, nil, result)
	return err
}
