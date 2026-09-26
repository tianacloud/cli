package authclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// The shared SDK does not expose exchange for a previously started transaction.
func (c *Client) exchangeAuthorizationCode(ctx context.Context, transaction AuthTransaction, code string) (credential Credential, resultErr error) {
	if transaction.ID == "" || transaction.ClientSecret == "" || code == "" {
		return Credential{}, errors.New("authentication transaction is incomplete")
	}
	payload, err := json.Marshal(map[string]string{"grant_type": "auth_transaction", "transaction_id": transaction.ID, "authorization_code": code, "client_secret": transaction.ClientSecret})
	if err != nil {
		return Credential{}, errors.New("cannot encode authorization exchange")
	}
	defer clear(payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Origin()+"/api/v1/auth/token", bytes.NewReader(payload))
	if err != nil {
		return Credential{}, errors.New("cannot create authorization exchange")
	}
	var identity [18]byte
	if _, err := rand.Read(identity[:]); err != nil {
		return Credential{}, errors.New("cannot generate authorization request ID")
	}
	requestID := "req-" + base64.RawURLEncoding.EncodeToString(identity[:])
	request.Header.Set("X-Request-ID", requestID)
	if c.config.OnRequestID != nil {
		func() { defer func() { _ = recover() }(); c.config.OnRequestID(requestID) }()
	}
	defer func() {
		if resultErr != nil {
			resultErr = fmt.Errorf("%w (Request ID: %s)", resultErr, requestID)
		}
	}()
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Cache-Control", "no-store")
	client := http.Client{Timeout: 25 * time.Second}
	if c.config.HTTPClient != nil {
		client = *c.config.HTTPClient
	}
	if c.config.RootCAs != nil || c.config.InsecureTLS {
		base := client.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		transport, ok := base.(*http.Transport)
		if !ok {
			return Credential{}, errors.New("custom trust requires an HTTP transport")
		}
		transport = transport.Clone()
		transport.TLSClientConfig = &tls.Config{RootCAs: c.config.RootCAs, MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.config.InsecureTLS}
		client.Transport = transport
		defer transport.CloseIdleConnections()
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return Credential{}, errors.New("authorization exchange outcome is unknown; start a new login")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Credential{}, errors.New("authorization exchange rejected; start a new login")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	defer clear(body)
	if err != nil || len(body) > 1<<20 {
		return Credential{}, errors.New("invalid authorization response")
	}
	var value struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		User         User   `json:"user"`
	}
	if json.Unmarshal(body, &value) != nil || value.AccessToken == "" || value.RefreshToken == "" || value.ExpiresIn <= 0 || value.ExpiresIn > 365*24*3600 || value.User.ID == "" {
		return Credential{}, errors.New("incomplete authorization response")
	}
	if value.TokenType == "" {
		value.TokenType = "Bearer"
	}
	if value.TokenType != "Bearer" {
		return Credential{}, errors.New("unsupported authorization token type")
	}
	return Credential{AccessToken: value.AccessToken, RefreshToken: value.RefreshToken, TokenType: value.TokenType, ExpiresAt: c.config.Now().Add(time.Duration(value.ExpiresIn) * time.Second), User: value.User}, nil
}

// AwaitAuthTransaction completes an isolated browser authorization in memory.
func (c *Client) AwaitAuthTransaction(ctx context.Context, transaction AuthTransaction) (Credential, error) {
	ctx, cancel := context.WithDeadline(ctx, transaction.CreatedAt.Add(time.Duration(transaction.ExpiresIn)*time.Second))
	defer cancel()
	for {
		result, err := c.PollAuthTransaction(ctx, transaction)
		if err != nil {
			return Credential{}, err
		}
		if result.Status == "approved" {
			credential, err := c.exchangeAuthorizationCode(ctx, transaction, result.AuthorizationCode)
			if err != nil {
				return Credential{}, err
			}
			if err = c.config.Store.Save(credential); err != nil {
				return Credential{}, err
			}
			return credential, nil
		}
		interval := result.RetryAfter
		if interval <= 0 {
			interval = transaction.PollInterval
		}
		if interval <= 0 {
			interval = time.Second
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Credential{}, ctx.Err()
		case <-timer.C:
		}
	}
}
