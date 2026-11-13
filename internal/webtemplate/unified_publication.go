package webtemplate

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tianacloud/cli/internal/fileclient"
)

var ErrPublicationFailed = errors.New("publication explicitly failed")

type TemplatePublication struct {
	TemplateID       string  `json:"template_id"`
	JobID            uint64  `json:"job_id"`
	Status           string  `json:"status"`
	LastError        string  `json:"last_error,omitempty"`
	PublishedVersion *string `json:"published_version"`
}

// Upload authorization is kept out of user-facing receipts.
type publicationUpload struct {
	TemplateID       string           `json:"template_id"`
	JobID            uint64           `json:"job_id"`
	Status           string           `json:"status"`
	LastError        string           `json:"last_error,omitempty"`
	Form             *fileclient.Form `json:"form,omitempty"`
	PublishedVersion *string          `json:"published_version"`
}

func (p publicationUpload) receipt() TemplatePublication {
	return TemplatePublication{TemplateID: p.TemplateID, JobID: p.JobID, Status: p.Status, LastError: p.LastError, PublishedVersion: p.PublishedVersion}
}
func validatePublication(receipt TemplatePublication, id string, job uint64) error {
	if receipt.TemplateID != id || receipt.JobID == 0 || (job != 0 && receipt.JobID != job) {
		return errors.New("publication receipt identity does not match")
	}
	switch receipt.Status {
	case "uploading", "pending", "running", "fail", "complete":
	default:
		return errors.New("unknown publication status; query the original job")
	}
	if receipt.PublishedVersion != nil && (!validDecimal(*receipt.PublishedVersion) || *receipt.PublishedVersion == "0" || receipt.Status != "complete") {
		return errors.New("publication version/status is inconsistent; query the original job")
	}
	if receipt.Status == "complete" && receipt.PublishedVersion == nil {
		return errors.New("completed publication has no version; query the original job")
	}
	return nil
}

func (c Client) PublicationStatus(ctx context.Context, id string, job uint64) (TemplatePublication, error) {
	path, err := unifiedTemplatePath(id)
	if err != nil {
		return TemplatePublication{}, err
	}
	if job == 0 {
		return TemplatePublication{}, errors.New("provide the original publication job identity")
	}
	var result publicationUpload
	_, err = c.Auth.DoJSON(ctx, "GET", path+"/publications/"+strconv.FormatUint(job, 10), nil, nil, &result)
	receipt := result.receipt()
	if err == nil {
		err = validatePublication(receipt, id, job)
	}
	return receipt, err
}
func (c Client) PublishTemplate(ctx context.Context, id, requestID, filename string) (TemplatePublication, error) {
	path, err := unifiedTemplatePath(id)
	if err != nil {
		return TemplatePublication{}, err
	}
	if strings.TrimSpace(requestID) == "" {
		return TemplatePublication{}, errors.New("provide a stable publication request identity")
	}
	f, err := os.Open(filename)
	if err != nil {
		return TemplatePublication{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return TemplatePublication{}, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return TemplatePublication{}, errors.New("install.zip must be a nonempty regular file")
	}
	pinned, err := c.Auth.PinCredential(ctx)
	if err != nil {
		return TemplatePublication{}, err
	}
	c.Auth = pinned
	var upload publicationUpload
	_, err = c.Auth.DoJSON(ctx, "POST", path+"/uploads", struct {
		RequestID string `json:"request_id"`
		Size      int64  `json:"size"`
	}{requestID, info.Size()}, nil, &upload)
	if err != nil {
		return upload.receipt(), fmt.Errorf("template %s request %s: %w; recover the original publication before any new upload", id, requestID, err)
	}
	receipt := upload.receipt()
	if err := validatePublication(receipt, id, 0); err != nil {
		return receipt, err
	}
	job := receipt.JobID
	switch receipt.Status {
	case "fail":
		return receipt, fmt.Errorf("%w: publication %d", ErrPublicationFailed, job)
	case "pending", "running", "complete":
		// Replayed begin returns the original job; never upload it again.
		return receipt, nil
	}

	if upload.Form == nil {
		return receipt, fmt.Errorf("template %s publication %d has no upload authorization; query the original job", id, job)
	}
	uploader := fileclient.Client{HTTP: c.Auth.FileHTTPClient()}
	uploadErr := uploader.Upload(ctx, *upload.Form, "install.zip", info.Size(), f)
	var complete publicationUpload
	_, err = c.Auth.DoJSON(ctx, "POST", path+"/uploads/"+strconv.FormatUint(job, 10)+"/complete", struct{}{}, nil, &complete)
	if err != nil {
		if uploadErr != nil {
			return receipt, fmt.Errorf("template %s publication %d upload/completion uncertain; query the original job", id, job)
		}
		return receipt, fmt.Errorf("template %s publication %d: %w; query the original job", id, job, err)
	}
	if err := validatePublication(complete.receipt(), id, job); err != nil {
		return receipt, fmt.Errorf("template %s publication %d completion identity changed; query the original job", id, job)
	}
	if complete.Status == "uploading" {
		return receipt, fmt.Errorf("template %s publication %d remains uploading; query or recover the original job", id, job)
	}
	if complete.Status == "fail" {
		return complete.receipt(), fmt.Errorf("%w: publication %d", ErrPublicationFailed, job)
	}
	return complete.receipt(), nil
}
func (c Client) WaitTemplatePublication(ctx context.Context, id string, job uint64) (TemplatePublication, error) {
	pinned, err := c.Auth.PinCredential(ctx)
	if err != nil {
		return TemplatePublication{}, err
	}
	c.Auth = pinned
	for {
		receipt, err := c.PublicationStatus(ctx, id, job)
		if err != nil {
			return receipt, err
		}
		if receipt.Status == "complete" && receipt.PublishedVersion != nil {
			return receipt, nil
		}
		if receipt.Status == "fail" {
			return receipt, fmt.Errorf("%w: publication %d", ErrPublicationFailed, job)
		}
		if receipt.Status != "uploading" && receipt.Status != "pending" && receipt.Status != "running" {
			return receipt, errors.New("publication result is unknown or incomplete; query the original job before any new submission")
		}

		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return receipt, ctx.Err()
		case <-timer.C:
		}
	}
}

type sourceDownload struct {
	TemplateID  string `json:"template_id"`
	SourceWebID string `json:"source_web_id"`
	Version     string `json:"version"`
	URL         string `json:"url"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	Prompt      string `json:"prompt"`
	Name        string `json:"name"`
}

func (c Client) InitUnifiedTemplate(ctx context.Context, id, channel, version, target string) (Result, error) {
	if _, err := unifiedTemplatePath(id); err != nil {
		return Result{}, err
	}
	if channel != "share" && channel != "market" {
		return Result{}, errors.New("channel must be share or market")
	}
	if version != "" && (!validDecimal(version) || version == "0") {
		return Result{}, errors.New("version must be a positive decimal string")
	}
	if err := requireAbsent(target); err != nil {
		return Result{}, err
	}
	query := url.Values{"channel": {channel}}
	if version != "" {
		query.Set("version", version)
	}
	var descriptor sourceDownload
	_, err := c.Auth.DoJSON(ctx, "GET", "/api/v1/template-catalog/"+url.PathEscape(id)+"/source?"+query.Encode(), nil, nil, &descriptor)
	if err != nil {
		return Result{}, err
	}
	if descriptor.TemplateID != id || descriptor.SourceWebID == "" || !validDecimal(descriptor.Version) || descriptor.Version == "0" || (version != "" && descriptor.Version != version) {
		return Result{}, errors.New("source descriptor identity or version does not match")
	}
	result, err := c.downloadSource(ctx, descriptor, target)
	if err != nil {
		return Result{}, err
	}
	result.TemplateID, result.SourceWebID, result.Version = id, descriptor.SourceWebID, descriptor.Version
	return result, nil
}
