package authclient

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tianacloud/cli/internal/localfile"
	"github.com/tianacloud/cli/internal/localstate"
	"os"
	"time"
)

var (
	ErrAuthorizationPending = errors.New("browser authorization is pending")
	ErrLoginNotStarted      = errors.New("no pending browser login")
	ErrCredentialSaveFailed = errors.New("cannot safely persist login state")
)

type pendingLogin struct {
	Transaction  AuthTransaction `json:"transaction"`
	ClientSecret string          `json:"client_secret"`
	Exchanging   bool            `json:"exchanging,omitempty"`
	Credential   *Credential     `json:"credential,omitempty"`
}

// StartLogin persists an authorization transaction and returns without polling.
func (c *Client) StartLogin(ctx context.Context) (AuthTransaction, error) {
	unlock, err := c.lockPendingLogin(ctx)
	if err != nil {
		return AuthTransaction{}, errors.Join(ErrCredentialSaveFailed, err)
	}
	defer unlock()
	pending, err := c.loadPendingLogin()
	if err == nil && c.config.Now().Before(pending.Transaction.CreatedAt.Add(time.Duration(pending.Transaction.ExpiresIn)*time.Second)) {
		if pending.Exchanging && pending.Credential == nil {
			if err := c.clearPendingLogin(); err != nil {
				return AuthTransaction{}, err
			}
		} else {
			return pending.Transaction, nil
		}
	} else if err != nil && !errors.Is(err, ErrLoginNotStarted) {
		return AuthTransaction{}, err
	}
	transaction, err := c.CreateAuthTransaction(ctx)
	if err != nil {
		return AuthTransaction{}, err
	}
	pending = pendingLogin{Transaction: transaction, ClientSecret: transaction.ClientSecret}
	if err := c.savePendingLogin(pending); err != nil {
		return AuthTransaction{}, err
	}
	return transaction, nil
}

// ResumeLogin polls once and saves credentials only after browser approval.
func (c *Client) ResumeLogin(ctx context.Context) (AuthTransaction, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	unlock, err := c.lockPendingLogin(ctx)
	if err != nil {
		return AuthTransaction{}, errors.Join(ErrCredentialSaveFailed, err)
	}
	defer unlock()
	pending, err := c.loadPendingLogin()
	if err != nil {
		return AuthTransaction{}, err
	}
	transaction := pending.Transaction
	if pending.Credential == nil {
		if !c.config.Now().Before(transaction.CreatedAt.Add(time.Duration(transaction.ExpiresIn) * time.Second)) {
			return transaction, errors.Join(ErrTransactionExpired, c.clearPendingLogin())
		}
		if pending.Exchanging {
			return transaction, errors.Join(ErrTransactionCompleted, c.clearPendingLogin())
		}
		result, err := c.PollAuthTransaction(ctx, transaction)
		if err != nil {
			if errors.Is(err, ErrTransactionExpired) || errors.Is(err, ErrTransactionDenied) || errors.Is(err, ErrTransactionCompleted) {
				return transaction, errors.Join(err, c.clearPendingLogin())
			}
			return transaction, err
		}
		if result.Status != "approved" {
			return transaction, ErrAuthorizationPending
		}
		// An interrupted exchange must not replay a one-time authorization code.
		pending.Exchanging = true
		if err := c.savePendingLogin(pending); err != nil {
			return transaction, err
		}
		credential, err := c.exchangeAuthorizationCode(ctx, transaction, result.AuthorizationCode)
		if err != nil {
			return transaction, errors.Join(err, c.clearPendingLogin())
		}
		pending.Credential = &credential
		if err := c.savePendingLogin(pending); err != nil {
			return transaction, err
		}
	}
	if err := c.config.Store.Save(*pending.Credential); err != nil {
		path, _ := c.pendingLoginPath()
		if store, ok := c.config.Store.(*FileStore); ok {
			path = store.Path
		}
		return transaction, errors.Join(ErrCredentialSaveFailed, localstate.Wrap("save account credential", path, err))
	}
	return transaction, c.clearPendingLogin()
}

func (c *Client) pendingLoginPath() (string, error) {
	store, ok := c.config.Store.(*FileStore)
	if !ok || store.Path == "" {
		return "", ErrCredentialSaveFailed
	}
	return fmt.Sprintf("%s.login-%x.json", store.Path, sha256.Sum256([]byte(c.Origin()))), nil
}

func (c *Client) loadPendingLogin() (pendingLogin, error) {
	path, err := c.pendingLoginPath()
	if err != nil {
		return pendingLogin{}, err
	}
	contents, err := localfile.Read(path, maxPendingBytes, true)
	defer clear(contents)
	if errors.Is(err, os.ErrNotExist) {
		return pendingLogin{}, ErrLoginNotStarted
	}
	if err != nil {
		return pendingLogin{}, errors.Join(ErrCredentialSaveFailed, localstate.Wrap("read pending authorization", path, err))
	}
	var pending pendingLogin
	if json.Unmarshal(contents, &pending) != nil || pending.Transaction.ID == "" || pending.ClientSecret == "" {
		return pendingLogin{}, errors.Join(ErrCredentialSaveFailed, localstate.Wrap("validate pending authorization", path, errors.New("invalid authorization state")))
	}
	pending.Transaction.ClientSecret = pending.ClientSecret
	return pending, nil
}

func (c *Client) savePendingLogin(pending pendingLogin) error {
	path, err := c.pendingLoginPath()
	if err != nil {
		return err
	}
	contents, err := json.Marshal(pending)
	if err != nil {
		return ErrCredentialSaveFailed
	}
	defer clear(contents)
	if len(contents) > maxPendingBytes {
		return ErrCredentialSaveFailed
	}
	if err := validatePendingFile(path); err != nil {
		return err
	}
	if err := atomicWritePrivate(path, contents); err != nil {
		return errors.Join(ErrCredentialSaveFailed, localstate.Wrap("save pending authorization", path, err))
	}
	return nil
}

func (c *Client) clearPendingLogin() error {
	path, err := c.pendingLoginPath()
	if err != nil {
		return err
	}
	if err := validatePendingFile(path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Join(ErrCredentialSaveFailed, localstate.Wrap("remove pending authorization", path, err))
	}
	return nil
}

func (c *Client) lockPendingLogin(ctx context.Context) (func(), error) {
	path, err := c.pendingLoginPath()
	if err != nil {
		return nil, err
	}
	return NewFilePendingCommandStore(path).Acquire(ctx)
}

// Logout also cancels this origin's unfinished conversational login locally.
func (c *Client) Logout(ctx context.Context) error {
	if _, ok := c.config.Store.(*FileStore); !ok {
		return c.Client.Logout(ctx)
	}
	unlock, err := c.lockPendingLogin(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if err := c.clearPendingLogin(); err != nil {
		return err
	}
	return c.Client.Logout(ctx)
}
