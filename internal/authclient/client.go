package authclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/tianacloud/cli/internal/buildconfig"
	"github.com/tianacloud/sdk-go/auth"
	"time"
)

type Config = auth.Config
type Credential = auth.Credential
type User = auth.User
type AuthTransaction = auth.AuthTransaction
type PollResult = auth.PollResult
type BrowserAuthAction = auth.BrowserAuthAction
type UserActionRequired = auth.UserActionRequired
type APIError = auth.APIError
type AuthenticatedOperation = auth.AuthenticatedOperation

var (
	ErrAuthenticationRequired = auth.ErrAuthenticationRequired
	ErrTransactionExpired     = auth.ErrTransactionExpired
	ErrTransactionDenied      = auth.ErrTransactionDenied
	ErrTransactionCompleted   = auth.ErrTransactionCompleted
	ErrInstanceNotFound       = errors.New("instance not found")
	ErrInstanceInvalidID      = errors.New("instance ID is invalid")
)

// Client keeps CLI resource operations around the shared SDK auth client.
type Client struct {
	*auth.Client
	config     Config
	credential *Credential
}

// WithCredential keeps this operation's requests in the space selected at entry.
// Each request gets a fresh one-use store; a 401 cannot adopt another login.
func (c *Client) WithCredential(credential Credential) *Client {
	copy := *c
	copy.credential = &credential
	return &copy
}

func (c *Client) DoJSON(ctx context.Context, method, path string, body any, headers map[string]string, result any) (int, error) {
	if c.credential == nil {
		return c.Client.DoJSON(ctx, method, path, body, headers, result)
	}
	cfg := c.config
	cfg.Store = &pinnedAppCredential{value: *c.credential}
	client, err := auth.NewWithConfig(cfg)
	if err != nil {
		return 0, err
	}
	return client.DoJSON(ctx, method, path, body, headers, result)
}

func New(origin string) (*Client, error) { return NewWithConfig(Config{Origin: origin}) }
func NewWithConfig(config Config) (*Client, error) {
	config.InsecureTLS = config.InsecureTLS || buildconfig.TLSInsecure()
	if config.Store == nil {
		var err error
		config.Store, err = auth.NewCredentialStore(config.Origin)
		if err != nil {
			return nil, err
		}
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	client, err := auth.NewWithConfig(config)
	if err != nil {
		return nil, err
	}
	return &Client{Client: client, config: config}, nil
}

type CreateInstanceRequest struct {
	SourceDir     string                 `json:"source_dir,omitempty"`
	GitInstanceID string                 `json:"git_instance_id,omitempty"`
	RequestID     string                 `json:"request_id"`
	DisplayName   string                 `json:"display_name"`
	Labels        map[string]string      `json:"labels,omitempty"`
	Notes         string                 `json:"notes,omitempty"`
	Engine        string                 `json:"engine"`
	Config        map[string]interface{} `json:"config"`
}

func NewIdempotencyKey() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("generate idempotency key")
	}
	return "cli-create-" + hex.EncodeToString(raw), nil
}
