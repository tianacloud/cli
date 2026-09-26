package appbootstrap

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/supervisor"
)

// NewConsoleLogin gives each browser authorization a separate in-memory MGR
// session. It neither replaces the CLI account nor sends account tokens to JS.
func NewConsoleLogin(config authclient.Config, instanceID string) StartLogin {
	return func(ctx context.Context) (LoginFlow, error) {
		cfg := config
		cfg.Store = &previewCredentials{}
		cfg.Output = nil
		cfg.NonInteractive = true
		client, err := authclient.NewWithConfig(cfg)
		if err != nil {
			return LoginFlow{}, errors.New("cannot initialize Console authorization")
		}
		transaction, err := client.CreateAuthTransaction(ctx)
		if err != nil {
			return LoginFlow{}, errors.New("Console authorization unavailable")
		}
		verification, err := url.Parse(transaction.VerificationURIComplete)
		origin, originErr := url.Parse(cfg.Origin)
		if err != nil || originErr != nil || verification.Scheme != "https" || verification.Host != origin.Host || verification.User != nil || verification.Fragment != "" {
			return LoginFlow{}, errors.New("unexpected Console authorization origin")
		}
		return LoginFlow{VerificationURL: verification.String(), ExpiresAt: transaction.CreatedAt.Add(time.Duration(transaction.ExpiresIn) * time.Second), Complete: func(ctx context.Context) (Identity, error) {
			if _, err := client.AwaitAuthTransaction(ctx, transaction); err != nil {
				return Identity{}, errors.New("Console authorization did not complete")
			}
			user, err := client.Whoami(ctx)
			if err != nil || user.ID == "" {
				return Identity{}, errors.New("Console identity unavailable")
			}
			var mu sync.Mutex
			var cached Connection
			var mintErr error
			resolve := func(ctx context.Context) (Connection, error) {
				// One refresh rotation at a time for this browser's account session.
				mu.Lock()
				defer mu.Unlock()
				instance, err := client.GetInstance(ctx, instanceID)
				if err != nil || instance.ID != instanceID || instance.Engine != "sqlite" || instance.Connection == nil {
					return Connection{}, errors.New("database is not accessible to this account")
				}
				endpoint, err := supervisor.ParseEndpoint(instance.Connection.Hostname)
				if err != nil {
					return Connection{}, errors.New("invalid database endpoint")
				}
				if instance.Connection.URL != "" {
					published, e := supervisor.ParseEndpointURL(instance.Connection.URL)
					if e != nil || published.Hostname() != endpoint.Hostname() {
						return Connection{}, errors.New("inconsistent database connection URL")
					}
					endpoint = published
				}
				if instance.EndpointID != "" && instance.EndpointID != endpoint.ID() {
					return Connection{}, errors.New("inconsistent database endpoint ID")
				}
				if cached.Token != "" {
					expires, _ := time.Parse(time.RFC3339Nano, cached.ExpiresAt)
					if expires.After(time.Now().Add(30 * time.Second)) {
						return cached, nil
					}
				}
				if mintErr != nil {
					return Connection{}, mintErr
				}
				requestID, err := authclient.NewIdempotencyKey()
				if err != nil {
					return Connection{}, err
				}
				expires := time.Now().Add(10 * time.Minute).Unix()
				var credential struct {
					TenantID   string `json:"tenant_id"`
					InstanceID string `json:"instance_id"`
					EndpointID string `json:"endpoint_id"`
					Token      string `json:"token"`
					Status     string `json:"status"`
					SyncStatus string `json:"sync_status"`
					ExpiresAt  int64  `json:"expires_at"`
				}
				route := "/api/v1/instances/" + url.PathEscape(instanceID) + "/endpoints/" + url.PathEscape(endpoint.ID()) + "/tokens"
				_, err = client.DoJSON(ctx, http.MethodPost, route, map[string]any{"request_id": requestID, "name": "local-app-preview", "expires_at": expires}, map[string]string{"Idempotency-Key": requestID}, &credential)
				if err != nil || credential.TenantID != user.TenantID || credential.InstanceID != instanceID || credential.EndpointID != endpoint.ID() || credential.Token == "" || credential.Status != "ACTIVE" || credential.SyncStatus != "complete" || credential.ExpiresAt > expires || credential.ExpiresAt <= time.Now().Add(30*time.Second).Unix() {
					mintErr = errors.New("preview database authorization is unavailable; sign out and authorize again after checking instance status")
					return Connection{}, mintErr
				}
				cached = Connection{InstanceID: instanceID, Origin: endpoint.URL(), SQLAPI: "hrana-v3", Token: credential.Token, ExpiresAt: time.Unix(credential.ExpiresAt, 0).UTC().Format(time.RFC3339Nano)}
				return cached, nil
			}
			// Do not release app assets until the Console account may access its database.
			if _, err = resolve(ctx); err != nil {
				return Identity{}, err
			}
			label := user.DisplayName
			if label == "" {
				label = user.Username
			}
			if label == "" {
				label = user.ID
			}
			return Identity{ID: user.ID, Label: label, Connection: resolve}, nil
		}}, nil
	}
}

type previewCredentials struct {
	mu    sync.Mutex
	value authclient.Credential
}

func (s *previewCredentials) Load() (authclient.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.value.AccessToken == "" {
		return authclient.Credential{}, authclient.ErrCredentialNotFound
	}
	return s.value, nil
}
func (s *previewCredentials) Save(value authclient.Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value = value
	return nil
}
func (s *previewCredentials) Delete() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value = authclient.Credential{}
	return nil
}
