package webtemplate

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// Candidate MGR user API; release requires integration with its exact implementation.
type UnifiedTemplate struct {
	TemplateID       string               `json:"template_id"`
	OwnerTenantID    string               `json:"owner_tenant_id"`
	SourceWebID      string               `json:"source_web_id"`
	SourceWebDeleted bool                 `json:"source_web_deleted"`
	Deleted          bool                 `json:"deleted"`
	DisplayName      string               `json:"display_name"`
	Description      string               `json:"description"`
	IsOfficial       bool                 `json:"is_official"`
	Blocked          bool                 `json:"blocked"`
	Revision         string               `json:"revision"`
	Distribution     TemplateDistribution `json:"distribution"`
	CurrentVersion   *TemplateVersion     `json:"current_version"`
	Permissions      TemplatePermissions  `json:"permissions"`
	Counters         TemplateCounters     `json:"counters"`
	UpdatedAt        int64                `json:"updated_at"`
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
type UnifiedPage struct {
	Items      []UnifiedTemplate `json:"items"`
	NextCursor string            `json:"next_cursor"`
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
