package clientconfig

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// ParseManagementOrigin accepts only an HTTPS origin without userinfo, path, query or fragment.
func ParseManagementOrigin(origin string) (string, error) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(origin, "#") || strings.TrimSpace(origin) != origin {
		return "", errors.New("management origin must be an HTTPS origin without credentials, path, query or fragment")
	}
	if port := parsed.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("management origin has an invalid port")
		}
	} else if strings.HasSuffix(parsed.Host, ":") {
		return "", errors.New("management origin has an invalid port")
	}
	return strings.TrimSuffix(origin, "/"), nil
}
