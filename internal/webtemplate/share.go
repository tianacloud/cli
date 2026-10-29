package webtemplate

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tianacloud/cli/internal/fileclient"
)

const maxInstallZIP = 32 << 20
const maxSourceZIP = 16 << 20

// Publication versions are decoded losslessly from MGR integers and printed as
// decimal strings, including values above JavaScript's safe integer range.
type publicationVersion uint64

func (v publicationVersion) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(strconv.FormatUint(uint64(v), 10))), nil
}

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

type Share struct {
	Version         publicationVersion `json:"version,omitempty"`
	MaterialVersion string             `json:"material_version,omitempty"`
	WebID           string             `json:"web_id"`
	PackageID       uint64             `json:"package_id"`
	URL             string             `json:"url"`
	HasSource       bool               `json:"has_source"`
	InstallCount    uint64             `json:"install_count"`
	Manifest        struct {
		Metadata           Template              `json:"metadata"`
		FormatVersion      int                   `json:"format_version,omitempty"`
		FileManifestSHA256 string                `json:"file_manifest_sha256,omitempty"`
		Requires           []string              `json:"requires,omitempty"`
		Files              *TemplateFileManifest `json:"files,omitempty"`
	} `json:"manifest"`
}
type ShareJob struct {
	JobID     uint64 `json:"job_id"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	LastError string `json:"last_error,omitempty"`
}
type ShareStatus struct {
	Share         Share      `json:"share"`
	Jobs          []ShareJob `json:"jobs"`
	StorageBytes  uint64     `json:"storage_bytes"`
	ReservedBytes uint64     `json:"reserved_bytes"`
}
type Publication struct {
	JobID     uint64           `json:"job_id"`
	Status    string           `json:"status"`
	LastError string           `json:"last_error,omitempty"`
	Form      *fileclient.Form `json:"form,omitempty"`
}

func shareAPI(web string) string {
	return "/api/v1/web-projects/" + url.PathEscape(web) + "/share-template"
}
func (c Client) ShareStatus(ctx context.Context, web string) (ShareStatus, error) {
	var result ShareStatus
	_, err := c.Auth.DoJSON(ctx, "GET", shareAPI(web), nil, nil, &result)
	return result, err
}
func (c Client) WithdrawShare(ctx context.Context, web string) (uint64, error) {
	var result struct {
		JobID uint64 `json:"cleanup_job_id"`
	}
	_, err := c.Auth.DoJSON(ctx, "DELETE", shareAPI(web), struct{}{}, nil, &result)
	return result.JobID, err
}
func (c Client) PublishShare(ctx context.Context, web, filename string) (Publication, error) {
	f, err := os.Open(filename)
	if err != nil {
		return Publication{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Publication{}, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxInstallZIP {
		return Publication{}, errors.New("install.zip must be a regular file of at most 32 MiB")
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return Publication{}, err
	}
	request := map[string]any{"request_id": "share-template-" + hex.EncodeToString(random[:]), "size": info.Size()}
	var result Publication
	if _, err = c.Auth.DoJSON(ctx, "POST", shareAPI(web)+"/uploads", request, nil, &result); err != nil {
		return result, err
	}
	if result.Form == nil {
		return result, fmt.Errorf("publication %d has no active upload authorization; inspect status-share-template", result.JobID)
	}
	uploader := fileclient.Client{HTTP: c.Auth.FileHTTPClient()}
	if err = uploader.Upload(ctx, *result.Form, "install.zip", info.Size(), f); err != nil {
		// A lost OSS response is resolved by HEAD in complete; it never authorizes a second key.
		original := err
		if _, err = c.Auth.DoJSON(ctx, "POST", fmt.Sprintf("%s/uploads/%d/complete", shareAPI(web), result.JobID), struct{}{}, nil, &result); err != nil {
			return result, fmt.Errorf("publication %d: %w; inspect status-share-template before starting another upload", result.JobID, original)
		}
		result.Form = nil
		return result, nil
	}
	_, err = c.Auth.DoJSON(ctx, "POST", fmt.Sprintf("%s/uploads/%d/complete", shareAPI(web), result.JobID), struct{}{}, nil, &result)
	result.Form = nil
	return result, err
}
func (c Client) WaitShare(ctx context.Context, web string, id uint64) (ShareStatus, error) {
	for {
		status, err := c.ShareStatus(ctx, web)
		if err != nil {
			return status, err
		}
		if status.Share.PackageID == id {
			return status, nil
		}
		found := false
		for _, j := range status.Jobs {
			if j.JobID == id {
				found = true
				if j.Status == "fail" {
					return status, fmt.Errorf("publication %d failed: %s", id, j.LastError)
				}
			}
		}
		if !found {
			return status, fmt.Errorf("publication %d is no longer current; inspect status-share-template", id)
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return status, ctx.Err()
		case <-timer.C:
		}
	}
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
	if shared.Scheme != origin.Scheme || shared.Host != origin.Host || shared.User != nil || shared.RawQuery != "" || shared.Fragment != "" || !strings.HasPrefix(shared.Path, "/shared-templates/") {
		return Result{}, errors.New("use a template share link from the current Tiana environment")
	}
	web := strings.TrimPrefix(shared.Path, "/shared-templates/")
	if web == "" || strings.Contains(web, "/") {
		return Result{}, errors.New("invalid template share link")
	}
	var descriptor struct {
		URL    string `json:"url"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
		Prompt string `json:"prompt"`
		Name   string `json:"name"`
	}
	_, err = c.Auth.DoJSON(ctx, "GET", "/api/v1/shared-templates/"+url.PathEscape(web)+"/source", nil, nil, &descriptor)
	if err != nil {
		return Result{}, err
	}
	if descriptor.Size <= 0 || descriptor.Size > maxSourceZIP {
		return Result{}, errors.New("source ZIP exceeds the 16 MiB limit")
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
