package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
	"github.com/urfave/cli/v3"
)

const thirdPartyManagementPath = "/api/v1/third-party-service"
const thirdPartyPropagationSeconds = 30

func newThirdPartyServiceCommand(output, diagnostics io.Writer) *cli.Command {
	leaf := func(name, usage, argument, operation string, paged bool, options ...cli.Flag) *cli.Command {
		flags := []cli.Flag{boolOption("json", "Write a structured JSON result")}
		if paged {
			flags = append(flags, &cli.IntFlag{Name: "page", Value: 1, Usage: "Page number"}, &cli.IntFlag{Name: "page-size", Value: 20, Usage: "Items per page"})
		}
		return &cli.Command{Name: name, Usage: usage, ArgsUsage: argument, Flags: append(flags, options...), Action: func(ctx context.Context, cmd *cli.Command) error {
			if (argument == "" && cmd.NArg() != 0) || (argument != "" && (cmd.NArg() != 1 || strings.TrimSpace(cmd.Args().First()) == "")) {
				return argumentFailure(ctx, cmd, "provide the displayed arguments")
			}
			if paged && (cmd.Int("page") < 1 || cmd.Int("page-size") < 1) {
				return argumentFailure(ctx, cmd, "page and page-size must be positive")
			}
			if (operation == "enable" || operation == "disable") && strings.TrimSpace(cmd.String("category")) == "" {
				return argumentFailure(ctx, cmd, "provide --category CATEGORY_SLUG")
			}
			if operation == "key-delete" {
				id, err := strconv.ParseUint(cmd.Args().First(), 10, 64)
				if err != nil || id == 0 {
					return argumentFailure(ctx, cmd, "provide the Key ID returned by key list")
				}
			}
			return statusError(runThirdPartyService(ctx, cmd, operation, output, diagnostics))
		}}
	}
	return &cli.Command{Name: "third-party-service", Usage: "Manage third-party services for the current space", Action: groupAction, Commands: []*cli.Command{
		{Name: "category", Usage: "Third-party service categories", Action: groupAction, Commands: []*cli.Command{
			leaf("list", "List categories, eligibility and activation", "", "category-list", true),
		}},
		{Name: "provider", Usage: "Third-party service providers", Action: groupAction, Commands: []*cli.Command{
			leaf("list", "List providers, icons and path prefixes", "", "provider-list", true),
		}},
		{Name: "api", Usage: "Third-party API catalogue and documentation", Action: groupAction, Commands: []*cli.Command{
			leaf("list", "List APIs by category or provider", "", "api-list", true, stringOption("category", "Category slug from category list", ""), stringOption("provider", "Provider slug from provider list", "")),
			leaf("show", "Show an API's routes, quota and compute price", "API_SLUG", "api-show", false),
			leaf("docs", "Write the complete API usage Markdown", "API_SLUG", "api-docs", false),
		}},
		leaf("enable", "Enable all APIs in a category for this space", "", "enable", false, stringOption("category", "Category slug from category list", "")),
		leaf("disable", "Disable all APIs in a category for this space", "", "disable", false, stringOption("category", "Category slug from category list", "")),
		{Name: "key", Usage: "Third-party API Keys for the current space", Action: groupAction, Commands: []*cli.Command{
			leaf("list", "List Key identities, masked values and status", "", "key-list", true),
			leaf("create", "Create a space API Key and write its full value", "NAME", "key-create", false),
			leaf("delete", "Revoke the specified space API Key", "KEY_ID", "key-delete", false),
		}},
	}}
}

type thirdPartyCategory struct {
	ID          uint64 `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Order       int    `json:"order"`
	MinimumTier uint16 `json:"minimum_tier"`
	Eligible    bool   `json:"eligible"`
	Enabled     bool   `json:"enabled"`
}

type thirdPartyProvider struct {
	ID     uint64 `json:"id"`
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	Icon   string `json:"icon"`
	Prefix string `json:"prefix"`
}

type thirdPartyAPI struct {
	ID           uint64          `json:"id"`
	Slug         string          `json:"slug"`
	Name         string          `json:"name"`
	ProviderID   uint64          `json:"provider_id"`
	CategoryID   uint64          `json:"category_id"`
	Rule         string          `json:"rule"`
	Quota        uint64          `json:"quota"`
	Period       string          `json:"period"`
	Price        json.RawMessage `json:"price"`
	Routes       json.RawMessage `json:"routes"`
	ProviderSlug string          `json:"provider_slug,omitempty"`
	CategorySlug string          `json:"category_slug,omitempty"`
}

type thirdPartyCatalog struct {
	Categories []thirdPartyCategory `json:"categories"`
	Providers  []thirdPartyProvider `json:"providers"`
	APIs       []thirdPartyAPI      `json:"apis"`
}

type thirdPartyDocument struct {
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	Markdown string `json:"markdown"`
}

type thirdPartyKey struct {
	ID        uint64 `json:"id"`
	Name      string `json:"name"`
	Value     string `json:"key"`
	CreatedAt int64  `json:"created_at"`
	RevokedAt *int64 `json:"revoked_at"`
	Status    string `json:"status,omitempty"`
}

func runThirdPartyService(ctx context.Context, cmd *cli.Command, operation string, output, diagnostics io.Writer) int {
	jsonMode := cmd.Bool("json")
	fail := func(err error) int {
		return thirdPartyReadFailure(ctx, err, jsonMode, output, diagnostics)
	}
	client, err := newAuthClient(ctx, diagnostics, true)
	if err != nil {
		return fail(err)
	}
	client, err = client.PinCredential(ctx)
	if err != nil {
		return fail(err)
	}
	request := func(method, path string, body, result any) error {
		_, err := client.DoJSON(ctx, method, thirdPartyManagementPath+path, body, nil, result)
		return err
	}
	display := func(result any) int { return writeAppResult(apppublish.Success(result), jsonMode, output, diagnostics) }
	argument := cmd.Args().First()
	page, pageSize := cmd.Int("page"), cmd.Int("page-size")
	switch operation {
	case "api-docs":
		var doc thirdPartyDocument
		if err := request(http.MethodGet, "/apis/"+url.PathEscape(argument)+"/docs", nil, &doc); err != nil {
			return fail(err)
		}
		if doc.Markdown == "" {
			return fail(errors.New("API usage documentation is unavailable"))
		}
		if jsonMode {
			return display(doc)
		}
		if _, err := io.WriteString(output, doc.Markdown); err != nil {
			return fail(err)
		}
		return 0
	case "enable", "disable":
		category := cmd.String("category")
		enabled := operation == "enable"
		if err := request(http.MethodPut, "/categories/"+url.PathEscape(category), map[string]bool{"enabled": enabled}, nil); err != nil {
			var api *authclient.APIError
			if enabled && errors.As(err, &api) && api.Code == "category_unavailable" {
				return writeAppResult(apppublish.Failure(&apppublish.Error{Code: "CATEGORY_UNAVAILABLE", Message: "This category is unavailable to the current space", ExitCode: 1}), jsonMode, output, diagnostics)
			}
			return thirdPartyWriteFailure(ctx, err, jsonMode, output, diagnostics)
		}
		if !jsonMode {
			_, err := fmt.Fprintf(output, "Saved category %s (%sd). Changes take effect within %d seconds.\n", safeDisplay(category), operation, thirdPartyPropagationSeconds)
			if err != nil {
				return fail(err)
			}
			return 0
		}
		return display(map[string]any{"category_id": category, "enabled": enabled, "propagation_seconds": thirdPartyPropagationSeconds})
	case "key-list":
		var keys []thirdPartyKey
		if err := request(http.MethodGet, "/keys", nil, &keys); err != nil {
			return fail(err)
		}
		for i := range keys {
			key := &keys[i]
			masked := "api-****************"
			if len(key.Value) == 20 && strings.HasPrefix(key.Value, "api-") {
				masked = "api-************" + key.Value[len(key.Value)-4:]
			}
			key.Value, key.Status = masked, "active"
			if key.RevokedAt != nil {
				key.Status = "revoked"
			}
		}
		return display(thirdPartyPage(keys, page, pageSize))
	case "key-create":
		var key thirdPartyKey
		if err := request(http.MethodPost, "/keys", map[string]string{"name": argument}, &key); err != nil {
			return thirdPartyWriteFailure(ctx, err, jsonMode, output, diagnostics)
		}
		if key.ID == 0 || key.Value == "" {
			return thirdPartyWriteFailure(ctx, errors.New("API Key creation returned no credential"), jsonMode, output, diagnostics)
		}
		return display(key)
	case "key-delete":
		if err := request(http.MethodDelete, "/keys/"+url.PathEscape(argument), nil, nil); err != nil {
			return thirdPartyWriteFailure(ctx, err, jsonMode, output, diagnostics)
		}
		if !jsonMode {
			if _, err := fmt.Fprintf(output, "Key %s revoked. Changes take effect within %d seconds.\n", safeDisplay(argument), thirdPartyPropagationSeconds); err != nil {
				return fail(err)
			}
			return 0
		}
		return display(map[string]any{"id": argument, "revoked": true, "propagation_seconds": thirdPartyPropagationSeconds})
	}
	category, provider := cmd.String("category"), cmd.String("provider")
	path := "/catalog"
	query := url.Values{}
	if category != "" {
		query.Set("category", category)
	}
	if provider != "" {
		query.Set("provider", provider)
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	var catalog thirdPartyCatalog
	if err := request(http.MethodGet, path, nil, &catalog); err != nil {
		return fail(err)
	}
	switch operation {
	case "category-list":
		return display(thirdPartyPage(catalog.Categories, page, pageSize))
	case "provider-list":
		return display(thirdPartyPage(catalog.Providers, page, pageSize))
	}
	categories, providers := make(map[uint64]string, len(catalog.Categories)), make(map[uint64]string, len(catalog.Providers))
	for _, candidate := range catalog.Categories {
		categories[candidate.ID] = candidate.Slug
	}
	for _, candidate := range catalog.Providers {
		providers[candidate.ID] = candidate.Slug
	}
	for i := range catalog.APIs {
		api := &catalog.APIs[i]
		api.CategorySlug, api.ProviderSlug = categories[api.CategoryID], providers[api.ProviderID]
		if operation == "api-show" && api.Slug == argument {
			return display(api)
		}
	}
	if operation == "api-show" {
		return fail(errors.New("API not found in the current space catalogue"))
	}
	return display(thirdPartyPage(catalog.APIs, page, pageSize))
}

func thirdPartyPage[T any](items []T, page, pageSize int) any {
	total := len(items)
	first, last, pages := total, total, 0
	if total != 0 {
		pages = 1 + (total-1)/pageSize
		if page <= pages {
			first = (page - 1) * pageSize
			last = first + min(pageSize, total-first)
		}
	}
	return struct {
		Items      []T `json:"items"`
		Page       int `json:"page"`
		PageSize   int `json:"page_size"`
		Total      int `json:"total"`
		TotalPages int `json:"total_pages"`
	}{append([]T{}, items[first:last]...), page, pageSize, total, pages}
}

func thirdPartyWriteFailure(ctx context.Context, err error, jsonMode bool, output, diagnostics io.Writer) int {
	var api *authclient.APIError
	if (errors.As(err, &api) && api.Status < http.StatusInternalServerError) || errors.Is(err, authclient.ErrAuthenticationRequired) {
		return thirdPartyReadFailure(ctx, err, jsonMode, output, diagnostics)
	}
	if jsonMode {
		return mutationJSONFailure(ctx, err, nil, output, diagnostics)
	}
	writeCommandError(diagnostics, err)
	code := 4
	if ctx.Err() != nil {
		code = 130
	}
	return writeAppResult(apppublish.Failure(&apppublish.Error{Code: "MUTATION_OUTCOME_UNKNOWN", Message: "Third-party service change outcome is unknown", NextAction: "Inspect the saved category or Key list before repeating the change", ExitCode: code}), false, output, diagnostics)
}

func thirdPartyReadFailure(ctx context.Context, err error, jsonMode bool, output, diagnostics io.Writer) int {
	if jsonMode {
		var api *authclient.APIError
		if errors.As(err, &api) && api.Code != "" {
			writeCommandError(diagnostics, err)
			return writeAppResult(apppublish.Failure(&apppublish.Error{Code: api.Code, Message: http.StatusText(api.Status), RequestID: api.RequestID, ExitCode: 1}), true, output, diagnostics)
		}
		return managementFailure(err, output, diagnostics, nil)
	}
	writeCommandError(diagnostics, err)
	if ctx.Err() != nil {
		return 130
	}
	return 1
}
