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
// session. Only its access token reaches JS; refresh tokens stay in the CLI.
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
			return accountIdentity(ctx, client, instanceID)
		}}, nil
	}
}

// NewAccountIdentity reuses the SDK's locked persistent account store. Only a
// launcher capability may install this identity in a browser session.
func NewAccountIdentity(ctx context.Context, cfg authclient.Config, instanceID string) (Identity, error) {
	cfg.NonInteractive = true
	cfg.Output = nil
	client, err := authclient.NewWithConfig(cfg)
	if err != nil {
		return Identity{}, err
	}
	return accountIdentity(ctx, client, instanceID)
}

func accountIdentity(ctx context.Context, client *authclient.Client, instanceID string) (Identity, error) {
	user, err := client.Whoami(ctx)
	if err != nil || user.ID == "" {
		return Identity{}, errors.New("Console identity unavailable")
	}
	var mu sync.Mutex
	var stopped error
	var flight *previewFlight
	// Only the broker performs management operations, serially. Once an SDK
	// operation fails, it may have consumed a rotating refresh token. Stop
	// this session rather than blindly retrying an ambiguous refresh.
	resolveOnce := func(ctx context.Context) (Connection, error) {
		if stopped != nil {
			return Connection{}, stopped
		}
		fail := func() (Connection, error) {
			stopped = errors.New("preview authorization unavailable; sign out and authorize again")
			return Connection{}, stopped
		}
		snapshot, err := client.EnsureCredential(ctx)
		if err != nil || snapshot.User.ID != user.ID || snapshot.User.TenantID != user.TenantID {
			return fail()
		}
		// SDK operations retain the persistent store's refresh lock. Another CLI
		// process may rotate between requests; reject a changed snapshot below
		// rather than releasing a token whose data-plane readiness was not checked.
		instance, err := client.GetInstance(ctx, instanceID)
		if err != nil || instance.ID != instanceID || instance.Engine != "sqlite" || instance.Connection == nil {
			return fail()
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
		// Login and refresh publish tenant grants asynchronously. Never expose
		// an access token until the current credential is ready in the data plane.
		for {
			var state struct {
				SyncStatus string `json:"sync_status"`
			}
			_, err = client.DoJSON(ctx, http.MethodGet, "/api/v1/auth/transactions/whoami", nil, nil, &state)
			if err != nil {
				return fail()
			}
			if state.SyncStatus == "complete" {
				break
			}
			if state.SyncStatus != "pending" {
				return fail()
			}
			timer := time.NewTimer(250 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return Connection{}, errors.New("preview authorization is still synchronizing")
			case <-timer.C:
			}
		}
		credential, err := client.LoadCredential()
		if err != nil || credential.AccessToken == "" || credential.User.ID != user.ID || credential.User.TenantID != user.TenantID || !credential.ExpiresAt.After(time.Now()) {
			return fail()
		}
		if credential.AccessToken != snapshot.AccessToken {
			return Connection{}, errors.New("preview account changed during synchronization; retry connection lookup")
		}
		return Connection{InstanceID: instanceID, Origin: endpoint.URL(), SQLAPI: "hrana-v3", Token: credential.AccessToken, ExpiresAt: credential.ExpiresAt.UTC().Format(time.RFC3339Nano)}, nil
	}
	resolve := func(ctx context.Context) (Connection, error) {
		if err := ctx.Err(); err != nil {
			return Connection{}, err
		}
		mu.Lock()
		if flight == nil {
			current := &previewFlight{done: make(chan struct{})}
			flight = current
			go func() {
				// A cancelled browser request must not cancel rotation after MGR
				// consumed the refresh token. Work is shared and independently bounded.
				workCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				current.connection, current.err = resolveOnce(workCtx)
				mu.Lock()
				close(current.done)
				flight = nil
				mu.Unlock()
			}()
		}
		current := flight
		mu.Unlock()
		select {
		case <-ctx.Done():
			return Connection{}, ctx.Err()
		case <-current.done:
			return current.connection, current.err
		}
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
}

type previewFlight struct {
	done       chan struct{}
	connection Connection
	err        error
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
