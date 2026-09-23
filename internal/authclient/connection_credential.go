package authclient

import (
	"context"
	"errors"
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
		origin := DefaultOrigin()
		if origin == "" {
			return nil, errors.New("set TIANA_MGR_ORIGIN and run tiana login, or provide TIANA_TOKEN/TIANA_TOKEN_FILE")
		}
		path := strings.TrimSpace(os.Getenv("TIANA_CREDENTIALS_FILE"))
		var store CredentialStore
		if path != "" {
			store = NewFileStore(path, origin)
		} else {
			var err error
			store, err = NewCredentialStore(origin)
			if err != nil {
				return nil, errors.New("cannot locate account credentials; run tiana login")
			}
		}
		account, err := store.Load()
		if errors.Is(err, ErrCredentialNotFound) {
			return nil, errors.New("not signed in; run tiana login or set TIANA_TOKEN/TIANA_TOKEN_FILE")
		}
		if err != nil {
			return nil, errors.New("cannot read account credentials; run tiana login")
		}
		if account.AccessToken == "" || !account.ExpiresAt.After(time.Now()) {
			return nil, errors.New("account access token is missing or expired; run tiana login")
		}
		value = []byte(account.AccessToken)
	}
	if len(value) == 0 || len(value) > MaxConnectionCredential || !httpguts.ValidHeaderFieldValue(string(value)) || strings.TrimSpace(string(value)) != string(value) {
		clear(value)
		return nil, errors.New("invalid connection credential from TIANA_TOKEN/TIANA_TOKEN_FILE or account session")
	}
	return value, nil
}
