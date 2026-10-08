package authclient

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tianacloud/cli/internal/localfile"
	"golang.org/x/net/http/httpguts"
)

// Matches the existing helper credential-frame bound. Secrets never enter
// diagnostics. This resolver only reads explicit input or the account store.
const MaxConnectionCredential = 512

func ConnectionCredential(ctx context.Context) ([]byte, error) {
	return connectionCredential(ctx, savedConnectionCredential)
}

// ConnectionCredential selects the same explicit inputs as other data connections.
// The fallback uses this client's origin/store and checks the pinned management identity;
// it never refreshes credentials or searches a local capability-token cache.
func (c *Client) ConnectionCredential(ctx context.Context, userID, tenantID string) ([]byte, error) {
	return connectionCredential(ctx, func(context.Context) ([]byte, error) {
		account, err := c.config.Store.Load()
		if err != nil || account.User.ID != userID || account.User.TenantID != tenantID {
			return nil, ErrAuthenticationRequired
		}
		return accountConnectionCredential(account, c.config.Now())
	})
}

func connectionCredential(ctx context.Context, account func(context.Context) ([]byte, error)) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	token, hasToken := os.LookupEnv("TIANA_TOKEN")
	file, hasFile := os.LookupEnv("TIANA_TOKEN_FILE")
	if hasToken && hasFile {
		return nil, errors.New("set only one of TIANA_TOKEN and TIANA_TOKEN_FILE")
	}
	var value []byte
	switch {
	case hasToken:
		value = []byte(token)
	case hasFile:
		var err error
		value, err = localfile.Read(file, MaxConnectionCredential+2, true)
		if err != nil {
			return nil, errors.New("cannot read TIANA_TOKEN_FILE: require an owned regular mode-0600 file without symlinks")
		}
		if len(value) > 0 && value[len(value)-1] == '\n' {
			value = value[:len(value)-1]
			if len(value) > 0 && value[len(value)-1] == '\r' {
				value = value[:len(value)-1]
			}
		}
	default:
		var err error
		value, err = account(ctx)
		if err != nil {
			return nil, err
		}
	}
	if len(value) == 0 || len(value) > MaxConnectionCredential || !httpguts.ValidHeaderFieldValue(string(value)) || strings.TrimSpace(string(value)) != string(value) {
		clear(value)
		return nil, errors.New("invalid connection credential from TIANA_TOKEN/TIANA_TOKEN_FILE or account session")
	}
	return value, nil
}

func savedConnectionCredential(ctx context.Context) ([]byte, error) {
	origin, err := ResolveOrigin(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w; or provide TIANA_TOKEN/TIANA_TOKEN_FILE", err)
	}
	store, err := NewCredentialStore(origin)
	if err != nil {
		return nil, errors.New("cannot locate account credentials; run tiana login")
	}
	account, err := store.Load()
	if errors.Is(err, ErrCredentialNotFound) {
		return nil, errors.New("not signed in; run tiana login or set TIANA_TOKEN/TIANA_TOKEN_FILE")
	}
	if err != nil {
		return nil, errors.New("cannot read account credentials; run tiana login")
	}
	return accountConnectionCredential(account, time.Now())
}

func accountConnectionCredential(account Credential, now time.Time) ([]byte, error) {
	if account.AccessToken == "" || !account.ExpiresAt.After(now) {
		return nil, errors.New("account access token is missing or expired; run tiana login")
	}
	return []byte(account.AccessToken), nil
}
