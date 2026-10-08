package webtemplate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

type Variable struct {
	Name        string   `json:"name"`
	Placeholder string   `json:"placeholder"`
	Description string   `json:"description"`
	Files       []string `json:"files"`
}
type Template struct {
	Name        string     `json:"name"`
	DisplayName string     `json:"display_name"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	Variables   []Variable `json:"variables,omitempty"`
}
type Page struct {
	Items      []Template `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
}
type download struct {
	Template
	Source      Source `json:"source"`
	ZIPSize     int64  `json:"zip_size"`
	ZIPSHA256   string `json:"zip_sha256"`
	DownloadURL string `json:"download_url"`
}
type Client struct {
	Auth         *authclient.Client
	DownloadHTTP *http.Client
}

func (c Client) List(ctx context.Context, after string) (Page, error) {
	var result Page
	query := url.Values{}
	if after != "" {
		query.Set("after", after)
	}
	requestPath := "/api/v1/web-templates"
	if len(query) > 0 {
		requestPath += "?" + query.Encode()
	}
	_, err := c.Auth.DoJSON(ctx, http.MethodGet, requestPath, nil, nil, &result)
	return result, err
}

func (c Client) Init(ctx context.Context, name, target string) (Result, error) {
	if err := requireAbsent(target); err != nil {
		return Result{}, err
	}
	var descriptor download
	_, err := c.Auth.DoJSON(ctx, http.MethodPost, "/api/v1/web-templates/"+url.PathEscape(name)+"/init", struct{}{}, nil, &descriptor)
	if err != nil {
		return Result{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, descriptor.DownloadURL, nil)
	if err != nil {
		return Result{}, errors.New("template download URL is invalid")
	}
	httpClient := c.DownloadHTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return Result{}, errors.New("template download failed; request a fresh download link and retry")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Result{}, errors.New("template download was rejected; request a fresh download link and retry")
	}
	absoluteTarget, err := filepath.Abs(target)
	if err != nil {
		return Result{}, err
	}
	if err = os.MkdirAll(filepath.Dir(absoluteTarget), 0755); err != nil {
		return Result{}, err
	}
	file, err := os.CreateTemp(filepath.Dir(absoluteTarget), ".tiana-template-*.zip")
	if err != nil {
		return Result{}, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), response.Body)
	if err != nil {
		return Result{}, errors.New("template download was interrupted")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if size != descriptor.ZIPSize || digest != descriptor.ZIPSHA256 {
		return Result{}, errors.New("template ZIP size or SHA-256 does not match")
	}
	result, err := Materialize(ctx, Archive{Name: name, Reader: file, Size: size}, target)
	if err != nil {
		return Result{}, err
	}
	result.ZIPSHA256 = digest
	return result, nil
}
