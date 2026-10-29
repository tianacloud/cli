package authclient

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type FunctionBuildLog struct {
	Text         string `json:"text"`
	DroppedBytes string `json:"dropped_bytes"`
}

// FunctionLogs reads the text response using the account already selected for
// this command. The JSON management request helper cannot decode build logs.
func (c *Client) FunctionLogs(ctx context.Context, instance, build string, credential Credential) (FunctionBuildLog, error) {
	var result FunctionBuildLog
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Origin()+"/api/v1/functions/"+url.PathEscape(instance)+"/builds/"+url.PathEscape(build)+"/logs", nil)
	if err != nil {
		return result, err
	}
	requestID := "req-" + rand.Text()
	request.Header.Set("X-Request-ID", requestID)
	request.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	request.Header.Set("Accept", "text/plain")
	if c.config.OnRequestID != nil {
		c.config.OnRequestID(requestID)
	}
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
			return result, errors.New("custom trust requires an HTTP transport")
		}
		transport = transport.Clone()
		transport.TLSClientConfig = &tls.Config{RootCAs: c.config.RootCAs, MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.config.InsecureTLS}
		client.Transport = transport
		defer transport.CloseIdleConnections()
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return result, fmt.Errorf("cannot read function build log (Request ID: %s)", requestID)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, &APIError{Status: response.StatusCode, RequestID: requestID, Message: http.StatusText(response.StatusCode)}
	}
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		return result, fmt.Errorf("incomplete function build log (Request ID: %s)", requestID)
	}
	result.Text = string(contents)
	result.DroppedBytes = response.Header.Get("X-Tiana-Dropped-Log-Bytes")
	return result, nil
}
