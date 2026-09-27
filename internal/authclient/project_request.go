package authclient

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/tianacloud/sdk-go/auth"
)

type pinnedProjectCredential struct {
	value  Credential
	loaded bool
	mu     sync.Mutex
}

func (s *pinnedProjectCredential) Load() (Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return Credential{}, ErrAuthenticationRequired
	}
	s.loaded = true
	return s.value, nil
}
func (s *pinnedProjectCredential) Save(Credential) error { return ErrAuthenticationRequired }
func (s *pinnedProjectCredential) Delete() error         { return ErrAuthenticationRequired }

// RequestProjectJSON pins a publication request to the already checked account.
func (c *Client) RequestProjectJSON(ctx context.Context, method, path string, body any, credential Credential, result any) (int, error) {
	if credential.AccessToken == "" || (path != "/api/v1/web-projects" && !strings.HasPrefix(path, "/api/v1/web-projects?after=") && !strings.HasPrefix(path, "/api/v1/web-projects/")) || strings.ContainsAny(path, "\\\r\n") {
		return 0, errors.New("invalid project request")
	}
	cfg := c.config
	cfg.Store = &pinnedProjectCredential{value: credential}
	client, err := auth.NewWithConfig(cfg)
	if err != nil {
		return 0, err
	}
	return client.DoJSON(ctx, method, path, body, nil, result)
}
