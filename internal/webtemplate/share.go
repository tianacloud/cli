package webtemplate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type TemplateFileEntry struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	ContentType string `json:"content_type"`
}
type TemplateFileManifest struct {
	Version int                 `json:"version"`
	Root    string              `json:"root"`
	Entries []TemplateFileEntry `json:"entries"`
}

func (c Client) InitShare(ctx context.Context, link, target string) (Result, error) {
	if err := requireAbsent(target); err != nil {
		return Result{}, err
	}
	shared, err := url.Parse(link)
	if err != nil {
		return Result{}, errors.New("invalid template share link")
	}
	origin, _ := url.Parse(c.Auth.Origin())
	if shared.Scheme != origin.Scheme || shared.Host != origin.Host || shared.User != nil || shared.Fragment != "" || !strings.HasPrefix(shared.Path, "/templates/") {
		return Result{}, errors.New("use a unified template link (/templates/TEMPLATE_ID) from the current Tiana environment")
	}
	channel := "share"
	if shared.RawQuery != "" {
		q := shared.Query()
		ch := q.Get("channel")
		if len(q) != 1 || (ch != "share" && ch != "market") {
			return Result{}, errors.New("use a template share link without unexpected query parameters")
		}
		channel = ch
	}
	id := strings.TrimPrefix(shared.Path, "/templates/")
	if id == "" || strings.Contains(id, "/") {
		return Result{}, errors.New("invalid template share link")
	}
	return c.InitUnifiedTemplate(ctx, id, channel, "", target)
}

func (c Client) downloadSource(ctx context.Context, descriptor sourceDownload, target string) (Result, error) {
	if descriptor.Size <= 0 {
		return Result{}, errors.New("source ZIP size must be positive")
	}
	client := http.Client{Timeout: 30 * time.Second}
	if c.DownloadHTTP != nil {
		client = *c.DownloadHTTP
	} else {
		client = *c.Auth.FileHTTPClient()
		client.Timeout = 30 * time.Second
	}
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, "GET", descriptor.URL, nil)
	if err != nil {
		return Result{}, errors.New("invalid source download URL")
	}
	response, err := client.Do(req)
	if err != nil {
		return Result{}, errors.New("source download failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("source download returned HTTP %d", response.StatusCode)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return Result{}, err
	}
	if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return Result{}, err
	}
	file, err := os.CreateTemp(filepath.Dir(target), ".tiana-source-*.zip")
	if err != nil {
		return Result{}, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(contextReader{ctx, response.Body}, descriptor.Size+1))
	if err != nil {
		return Result{}, err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if size != descriptor.Size || digest != descriptor.SHA256 {
		return Result{}, errors.New("source ZIP size or SHA-256 does not match")
	}
	result, err := Materialize(ctx, Archive{Name: descriptor.Name, Reader: file, Size: size}, target)
	if err != nil {
		return Result{}, err
	}
	result.ZIPSHA256 = digest
	result.Prompt = descriptor.Prompt
	return result, nil
}
