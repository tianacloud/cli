package authclient

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/tianacloud/sdk-go/auth"
)

type pinnedAppCredential struct {
	value  Credential
	loaded bool
	mu     sync.Mutex
}

func (s *pinnedAppCredential) Load() (Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return Credential{}, ErrAuthenticationRequired
	}
	s.loaded = true
	return s.value, nil
}
func (s *pinnedAppCredential) Save(Credential) error { return ErrAuthenticationRequired }
func (s *pinnedAppCredential) Delete() error         { return ErrAuthenticationRequired }

// RequestAppJSON pins a publication request to the already checked account.
func (c *Client) RequestAppJSON(ctx context.Context, method, path string, body any, credential Credential, result any) (int, error) {
	if credential.AccessToken == "" || (path != "/api/v1/apps" && !strings.HasPrefix(path, "/api/v1/apps?after=") && !strings.HasPrefix(path, "/api/v1/apps/")) || strings.ContainsAny(path, "\\\r\n") {
		return 0, errors.New("invalid app request")
	}
	cfg := c.config
	cfg.Store = &pinnedAppCredential{value: credential}
	client, err := auth.NewWithConfig(cfg)
	if err != nil {
		return 0, err
	}
	return client.DoJSON(ctx, method, path, body, nil, result)
}
