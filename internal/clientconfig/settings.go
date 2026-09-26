package clientconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/tianacloud/cli/internal/localfile"
)

// Settings selects the management endpoint for one invocation.
type Settings struct {
	ManagementOrigin string `json:"managementOrigin"`
}

type settingsKey struct{}

// WithSettings binds configuration without changing the process environment.
func WithSettings(ctx context.Context, settings Settings) context.Context {
	return context.WithValue(ctx, settingsKey{}, settings)
}

// ManagementOrigin returns the explicitly configured invocation origin.
func ManagementOrigin(ctx context.Context) string {
	settings, _ := ctx.Value(settingsKey{}).(Settings)
	return settings.ManagementOrigin
}

// LoadSettings reads a bounded public JSON configuration file.
func LoadSettings(path string) (Settings, error) {
	contents, err := localfile.Read(path, 64<<10, false)
	if err != nil {
		return Settings{}, errors.New("cannot read --config: expected a regular, non-symlink JSON file of at most 64 KiB")
	}
	var settings Settings
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		return Settings{}, errors.New("invalid --config JSON; expected managementOrigin")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Settings{}, errors.New("invalid --config JSON; expected one object")
	}
	settings.ManagementOrigin, err = ParseManagementOrigin(settings.ManagementOrigin)
	if err != nil {
		return Settings{}, err
	}
	return settings, nil
}

// ParseManagementOrigin accepts only an HTTPS origin without userinfo, path, query or fragment.
func ParseManagementOrigin(origin string) (string, error) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(origin, "#") || strings.TrimSpace(origin) != origin {
		return "", errors.New("managementOrigin must be an HTTPS origin without credentials, path, query or fragment")
	}
	if port := parsed.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("managementOrigin has an invalid port")
		}
	} else if strings.HasSuffix(parsed.Host, ":") {
		return "", errors.New("managementOrigin has an invalid port")
	}
	return strings.TrimSuffix(origin, "/"), nil
}
