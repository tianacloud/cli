package webtemplate

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

type DecimalUint64 uint64

func (d DecimalUint64) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(strconv.FormatUint(uint64(d), 10))), nil
}

func (d *DecimalUint64) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*d = 0
		return nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		var raw string
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		if raw == "" {
			*d = 0
			return nil
		}
		if !validDecimal(raw) {
			return errors.New("invalid decimal uint64 string")
		}
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return err
		}
		*d = DecimalUint64(v)
		return nil
	}
	var v uint64
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*d = DecimalUint64(v)
	return nil
}

type TemplateAuthor struct {
	PrincipalID string `json:"principal_id,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

func (a *TemplateAuthor) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		var name string
		if err := json.Unmarshal(data, &name); err != nil {
			return err
		}
		*a = TemplateAuthor{DisplayName: name}
		return nil
	}
	type plain TemplateAuthor
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*a = TemplateAuthor(value)
	return nil
}

type UnifiedTemplate struct {
	TemplateID               string               `json:"template_id"`
	OwnerTenantID            string               `json:"owner_tenant_id"`
	SourceWebID              string               `json:"source_web_id"`
	SourceWebDeleted         bool                 `json:"source_web_deleted"`
	Deleted                  bool                 `json:"deleted"`
	Author                   *TemplateAuthor      `json:"author,omitempty"`
	DisplayName              string               `json:"display_name"`
	Description              string               `json:"description"`
	SpaceName                string               `json:"space_name,omitempty"`
	IsOfficial               bool                 `json:"is_official"`
	Blocked                  bool                 `json:"blocked"`
	Revision                 string               `json:"revision"`
	CategoryID               DecimalUint64        `json:"category_id,omitempty"`
	Tags                     []string             `json:"tags,omitempty"`
	URL                      string               `json:"url,omitempty"`
	CoverURL                 string               `json:"cover_url,omitempty"`
	PreviewURL               string               `json:"preview_url,omitempty"`
	RiskConfirmationRequired bool                 `json:"risk_confirmation_required,omitempty"`
	Distribution             TemplateDistribution `json:"distribution"`
	CurrentVersion           *TemplateVersion     `json:"current_version"`
	Permissions              TemplatePermissions  `json:"permissions"`
	Counters                 TemplateCounters     `json:"counters"`
	FirstPublishedAt         int64                `json:"first_published_at,omitempty"`
	UpdatedAt                int64                `json:"updated_at"`
}
type TemplateCounters struct {
	ShareInstallCount  string `json:"share_install_count"`
	MarketInstallCount string `json:"market_install_count"`
	MarketCopyCount    string `json:"market_copy_count"`
}
type TemplatePermissions struct {
	CanPublishVersion bool `json:"can_publish_version"`
	CanChangeShare    bool `json:"can_change_share"`
	CanChangeMarket   bool `json:"can_change_market"`
	CanDelete         bool `json:"can_delete"`
	CanDownloadSource bool `json:"can_download_source"`
	CanEditMetadata   bool `json:"can_edit_metadata,omitempty"`
	CanInstall        bool `json:"can_install,omitempty"`
}
type TemplateDistribution struct {
	ShareEnabled  *bool `json:"share_enabled"`
	MarketEnabled *bool `json:"market_enabled"`
}
type TemplateVersion struct {
	Version         string `json:"version"`
	MaterialVersion string `json:"material_version"`
	HasSource       bool   `json:"has_source"`
	Installable     bool   `json:"installable"`
}
type DistributionUpdate struct {
	RequestID        string `json:"request_id"`
	ExpectedRevision string `json:"expected_revision"`
	ShareEnabled     *bool  `json:"share_enabled,omitempty"`
	MarketEnabled    *bool  `json:"market_enabled,omitempty"`
}
type DistributionState struct {
	Revision     string               `json:"revision"`
	Distribution TemplateDistribution `json:"distribution"`
}
type DistributionReceipt struct {
	TemplateID string            `json:"template_id"`
	RequestID  string            `json:"request_id"`
	Before     DistributionState `json:"before"`
	After      DistributionState `json:"after"`
	UpdatedAt  int64             `json:"updated_at"`
}
type DeletionReceipt struct {
	TemplateID   string `json:"template_id"`
	RequestID    string `json:"request_id"`
	Revision     string `json:"revision"`
	CleanupJobID uint64 `json:"cleanup_job_id"`
	Status       string `json:"status"`
}
type CreationPermissions struct {
	CanCreateTemplate bool `json:"can_create_template"`
}
type UnifiedPage struct {
	Items               []UnifiedTemplate    `json:"items"`
	NextCursor          string               `json:"next_cursor"`
	CreationPermissions *CreationPermissions `json:"creation_permissions,omitempty"`
}

func validDecimal(s string) bool {
	if s == "" || strings.TrimLeft(s, "0123456789") != "" || (len(s) > 1 && s[0] == '0') {
		return false
	}
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}
func unifiedTemplatePath(id string) (string, error) {
	if strings.TrimSpace(id) == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\\r\n") {
		return "", errors.New("provide a single template identity")
	}
	return "/api/v1/templates/" + url.PathEscape(id), nil
}
func ValidTemplateID(id string) bool { _, err := unifiedTemplatePath(id); return err == nil }
func ValidDecimal(s string) bool     { return validDecimal(s) }

func (d TemplateDistribution) valid() bool { return d.ShareEnabled != nil && d.MarketEnabled != nil }
func (c Client) TemplateStatus(ctx context.Context, id string) (UnifiedTemplate, error) {
	path, err := unifiedTemplatePath(id)
	if err != nil {
		return UnifiedTemplate{}, err
	}
	var result UnifiedTemplate
	_, err = c.Auth.DoJSON(ctx, "GET", path, nil, nil, &result)
	if err == nil && (result.TemplateID != id || !result.Distribution.valid()) {
		return UnifiedTemplate{}, errors.New("template receipt identity or distribution is invalid")
	}
	return result, err
}
func (c Client) TemplatesBySource(ctx context.Context, sourceWebID string) (UnifiedPage, error) {
	if strings.TrimSpace(sourceWebID) == "" || sourceWebID == "." || sourceWebID == ".." || strings.ContainsAny(sourceWebID, "/\\\r\n") {
		return UnifiedPage{}, errors.New("provide a valid source Web identity")
	}
	query := url.Values{"source_web_id": {sourceWebID}}
	var result UnifiedPage
	_, err := c.Auth.DoJSON(ctx, "GET", "/api/v1/templates?"+query.Encode(), nil, nil, &result)
	if err == nil && result.Items == nil {
		err = errors.New("template lookup response is missing items")
	}
	return result, err
}
func (c Client) CreateTemplate(ctx context.Context, web, requestID string) (UnifiedTemplate, error) {
	if strings.TrimSpace(web) == "" || strings.TrimSpace(requestID) == "" {
		return UnifiedTemplate{}, errors.New("source Web and request identity are required")
	}
	var result UnifiedTemplate
	_, err := c.Auth.DoJSON(ctx, "POST", "/api/v1/templates", struct {
		SourceWebID string `json:"source_web_id"`
		RequestID   string `json:"request_id"`
	}{web, requestID}, nil, &result)
	if err == nil && (result.TemplateID == "" || result.SourceWebID != web || !result.Distribution.valid()) {
		err = errors.New("template creation receipt is invalid; replay only the original request identity")
	}
	return result, err
}
func (c Client) ListTemplates(ctx context.Context, after string, limit int) (UnifiedPage, error) {
	if limit < 1 || limit > 100 {
		return UnifiedPage{}, errors.New("template limit must be between 1 and 100")
	}
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if after != "" {
		query.Set("after", after)
	}
	var result UnifiedPage
	_, err := c.Auth.DoJSON(ctx, "GET", "/api/v1/templates?"+query.Encode(), nil, nil, &result)
	if err == nil && result.Items == nil {
		err = errors.New("template list response is missing items")
	}
	return result, err
}
func (c Client) ListCatalog(ctx context.Context, channel, after, q, sort string, categoryID uint64, limit int) (UnifiedPage, error) {
	if channel != "market" && channel != "share" {
		return UnifiedPage{}, errors.New("channel must be market or share")
	}
	if sort != "" && sort != "downloads" && sort != "created_at" {
		return UnifiedPage{}, errors.New("sort must be downloads or created_at")
	}
	if limit < 0 || limit > 100 {
		return UnifiedPage{}, errors.New("template limit must be between 1 and 100")
	}
	query := url.Values{"channel": {channel}}
	if after != "" {
		query.Set("after", after)
	}
	if q != "" {
		query.Set("q", q)
	}
	if sort != "" {
		query.Set("sort", sort)
	}
	if categoryID != 0 {
		query.Set("category_id", strconv.FormatUint(categoryID, 10))
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	var result UnifiedPage
	_, err := c.Auth.DoJSON(ctx, "GET", "/api/v1/template-catalog?"+query.Encode(), nil, nil, &result)
	if err == nil && result.Items == nil {
		err = errors.New("template catalog response is missing items")
	}
	return result, err
}
func (c Client) SetTemplateDistribution(ctx context.Context, id string, update DistributionUpdate) (DistributionReceipt, error) {
	path, err := unifiedTemplatePath(id)
	if err != nil {
		return DistributionReceipt{}, err
	}
	if strings.TrimSpace(update.RequestID) == "" || !validDecimal(update.ExpectedRevision) || (update.ShareEnabled == nil && update.MarketEnabled == nil) {
		return DistributionReceipt{}, errors.New("provide request identity, expected revision and a distribution change")
	}
	var result DistributionReceipt
	_, err = c.Auth.DoJSON(ctx, "PUT", path+"/distribution", update, nil, &result)
	if err != nil {
		return result, err
	}
	if result.TemplateID != id || result.RequestID != update.RequestID || result.Before.Revision != update.ExpectedRevision || !validDecimal(result.After.Revision) || !result.Before.Distribution.valid() || !result.After.Distribution.valid() {
		return result, errors.New("distribution receipt is invalid; replay only the original request and expected revision")
	}
	before, after := result.Before.Distribution, result.After.Distribution
	if (update.ShareEnabled != nil && *after.ShareEnabled != *update.ShareEnabled) || (update.ShareEnabled == nil && *before.ShareEnabled != *after.ShareEnabled) || (update.MarketEnabled != nil && *after.MarketEnabled != *update.MarketEnabled) || (update.MarketEnabled == nil && *before.MarketEnabled != *after.MarketEnabled) {
		return result, errors.New("distribution receipt does not match requested switches")
	}
	return result, nil
}
func (c Client) DeleteTemplate(ctx context.Context, id, requestID, expectedRevision string) (DeletionReceipt, error) {
	path, err := unifiedTemplatePath(id)
	if err != nil {
		return DeletionReceipt{}, err
	}
	if strings.TrimSpace(requestID) == "" || !validDecimal(expectedRevision) {
		return DeletionReceipt{}, errors.New("provide request identity and expected revision")
	}
	var result DeletionReceipt
	_, err = c.Auth.DoJSON(ctx, "DELETE", path, struct {
		RequestID        string `json:"request_id"`
		ExpectedRevision string `json:"expected_revision"`
	}{requestID, expectedRevision}, nil, &result)
	if err != nil {
		return result, err
	}
	if result.TemplateID != id || result.RequestID != requestID {
		return result, errors.New("deletion receipt identity does not match; replay only the original request")
	}
	return result, nil
}
