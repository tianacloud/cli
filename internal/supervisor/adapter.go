package supervisor

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const SQLDAdapterID = "sqld"

// AdapterRegistry contains only explicitly reviewed adapters. The MVP ships
// one SQLD/libSQL adapter; no basename or future database protocol is inferred.
type AdapterRegistry struct {
	byID map[string]Adapter
}

func NewAdapterRegistry() *AdapterRegistry {
	registry := &AdapterRegistry{byID: make(map[string]Adapter)}
	registry.Register(SQLDAdapter{})
	return registry
}

func (r *AdapterRegistry) Register(adapter Adapter) {
	if r == nil || adapter == nil || adapter.ID() == "" {
		return
	}
	r.byID[adapter.ID()] = adapter
}

func (r *AdapterRegistry) Select(program, explicitID string) (Adapter, error) {
	if r == nil {
		return nil, ErrUnsupportedAdapter
	}
	if explicitID != "" {
		adapter, ok := r.byID[explicitID]
		if !ok || !adapter.Matches(program, true) {
			return nil, ErrUnsupportedAdapter
		}
		return adapter, nil
	}
	if strings.ContainsAny(program, `/\\`) {
		return nil, ErrUnsupportedClient
	}
	for _, adapter := range r.byID {
		if adapter.Matches(program, false) {
			return adapter, nil
		}
	}
	return nil, ErrUnsupportedClient
}

// SQLDAdapter is the registered HTTP/WebSocket native-client adapter. It
// accepts only the reviewed `turso db shell` grammar, replaces its remote URL,
// and scrubs route-affecting environment values. The helper process remains the
// classifier/relay owner.
type SQLDAdapter struct{}

func (SQLDAdapter) ID() string { return SQLDAdapterID }

func (SQLDAdapter) Matches(program string, explicit bool) bool {
	if program == "" {
		return false
	}
	name := filepath.Base(program)
	// The v1.0.32 Turso grammar is the only reviewed native shape. An unknown
	// libsql grammar must not be guessed from its basename.
	_ = explicit
	return name == "turso"
}

// ResolveEndpoint extracts the sole remote authority from Turso v1.0.32's
// reviewed `db shell <database-name|replica-url> [sql] [flags]` shape. Tiana
// deliberately accepts only a canonical HTTPS Endpoint URL, not a Turso Cloud
// database name or a heuristic locator found elsewhere in argv.
func (SQLDAdapter) ResolveEndpoint(argv []string) (Endpoint, error) {
	if len(argv) < 3 || filepath.Base(argv[0]) != "turso" || argv[1] != "db" || argv[2] != "shell" {
		return Endpoint{}, ErrUnsupportedClient
	}
	if len(argv) < 4 {
		return Endpoint{}, ErrInvalidEndpoint
	}
	endpoint, err := ParseEndpointURL(argv[3])
	if err != nil {
		return Endpoint{}, err
	}
	if err := validateTursoShellTail(argv[4:]); err != nil {
		return Endpoint{}, err
	}
	return endpoint, nil
}

func (SQLDAdapter) Prepare(endpoint LocalEndpoint, profile string, argv, environment []string) (PreparedCommand, error) {
	if len(argv) == 0 || argv[0] == "" {
		return PreparedCommand{}, ErrEmptyNativeClient
	}
	if _, err := (SQLDAdapter{}).ResolveEndpoint(argv); err != nil {
		return PreparedCommand{}, err
	}
	localURL, _, _, err := localTCPParts(endpoint, profile)
	if err != nil {
		return PreparedCommand{}, err
	}

	resultArgv := append([]string(nil), argv...)
	resultArgv[3] = localURL
	childEnv := allowlistedChildEnvironment(environment, "")
	childEnv = append(childEnv, "TURSO_DATABASE_URL="+localURL)
	return PreparedCommand{Program: resultArgv[0], Argv: resultArgv, Env: childEnv}, nil
}

func validateTursoShellTail(argv []string) error {
	for _, arg := range argv {
		switch arg {
		case "--proxy", "--proxy-url", "--proxy-command", "--instance", "--location", "--config-path", "-c", "--attach", "--remote-encryption-key", "--url", "--database-url", "--host", "--port", "--socket":
			return ErrRouteConflict
		}
		for _, prefix := range []string{
			"--proxy=", "--proxy-url=", "--proxy-command=", "--instance=", "--location=", "--config-path=",
			"--attach=", "--remote-encryption-key=", "--url=", "--database-url=", "--host=", "--port=", "--socket=",
		} {
			if strings.HasPrefix(arg, prefix) {
				return ErrRouteConflict
			}
		}
		// The SQLD-owned locator precedes this tail. Any other
		// locator-looking positional argument would make destination authority
		// ambiguous, including a second locator after an SQL expression.
		if looksLikeTursoLocator(arg) {
			return ErrRouteConflict
		}
	}
	return nil
}

func looksLikeTursoLocator(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") {
		return false
	}
	parsed, err := url.Parse(value)
	if err == nil && ((parsed.Scheme == "http" || parsed.Scheme == "https" || parsed.Scheme == "ws" || parsed.Scheme == "wss") && parsed.Host != "" || parsed.Scheme == "unix" && parsed.Path != "") || strings.HasPrefix(value, "/") {
		return true
	}
	if net.ParseIP(value) != nil {
		return true
	}
	_, err = ParseEndpoint(value)
	return err == nil
}

func localTCPParts(endpoint LocalEndpoint, profile ...string) (string, string, string, error) {
	if endpoint.Network != "tcp" {
		return "", "", "", fmt.Errorf("SQLD adapter requires a loopback TCP endpoint")
	}
	selectedProfile := ""
	if len(profile) > 1 {
		return "", "", "", fmt.Errorf("SQLD adapter received multiple profiles")
	}
	if len(profile) == 1 {
		selectedProfile = profile[0]
	}
	scheme, ok := profileScheme(selectedProfile)
	if !ok {
		return "", "", "", ErrUnsupportedProfile
	}
	parsed, err := url.Parse(endpoint.URL)
	if err != nil || parsed.Scheme != scheme || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", "", fmt.Errorf("helper returned an invalid local SQLD URL")
	}
	host := parsed.Hostname()
	port := parsed.Port()
	if port == "" || !isLoopbackHost(host) || endpoint.Address == "" {
		return "", "", "", fmt.Errorf("helper returned a non-loopback SQLD endpoint")
	}
	address := net.JoinHostPort(host, port)
	if endpoint.Address != address {
		return "", "", "", fmt.Errorf("helper returned mismatched SQLD locator")
	}
	return parsed.String(), host, port, nil
}

func isLoopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func sameLocalHost(value, expected string) bool {
	return net.ParseIP(value) != nil && value == expected
}

var routeEnvironmentNames = map[string]struct{}{
	"TURSO_DATABASE_URL": {}, "LIBSQL_URL": {}, "DATABASE_URL": {},
	"HTTP_PROXY": {}, "HTTPS_PROXY": {}, "ALL_PROXY": {}, "NO_PROXY": {},
	"http_proxy": {}, "https_proxy": {}, "all_proxy": {}, "no_proxy": {},
	"SOCKS_PROXY": {}, "socks_proxy": {}, "TURSO_PROXY": {}, "LIBSQL_PROXY": {},
}

func scrubChildEnvironment(environment []string) []string {
	return allowlistedChildEnvironment(environment, "")
}

func defaultChildEnvironment(tokenSource CredentialSource) []string {
	key := ""
	if tokenSource.Kind == CredentialFromEnvironment {
		key = tokenSource.Value
	}
	return allowlistedChildEnvironment(os.Environ(), key)
}

func helperEnvironment(environment []string, tokenSource CredentialSource) []string {
	key := ""
	if tokenSource.Kind == CredentialFromEnvironment {
		key = tokenSource.Value
	}
	result := allowlistedChildEnvironment(environment, key)
	filtered := result[:0]
	for _, entry := range result {
		name, _, _ := strings.Cut(entry, "=")
		if name == "TURSO_AUTH_TOKEN" || name == "LIBSQL_AUTH_TOKEN" {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// allowlistedChildEnvironment keeps only reviewed, non-routing process
// context. It preserves first-seen order and removes duplicate keys rather
// than sorting/reinterpreting the caller's environment. The exact credential
// source key is excluded even when it is not prefixed TIANA_.
func allowlistedChildEnvironment(environment []string, credentialKey string) []string {
	result := make([]string, 0, len(environment)+1)
	seen := make(map[string]struct{}, len(environment))
	for _, entry := range environment {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "" || key == credentialKey {
			continue
		}
		if _, duplicate := seen[key]; duplicate || !allowedEnvironmentKey(key) {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, entry)
	}
	return result
}

func allowedEnvironmentKey(key string) bool {
	if _, route := routeEnvironmentNames[key]; route || strings.HasPrefix(key, "TIANA_") {
		return false
	}
	switch key {
	case "PATH", "HOME", "USER", "LOGNAME", "SHELL", "TERM", "COLORTERM", "LANG", "LC_ALL", "LC_CTYPE", "LC_MESSAGES", "TZ", "TMPDIR", "TMP", "TEMP", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_RUNTIME_DIR", "SSL_CERT_FILE", "SSL_CERT_DIR", "TURSO_AUTH_TOKEN", "LIBSQL_AUTH_TOKEN":
		return true
	}
	return strings.HasPrefix(key, "LC_") || strings.HasPrefix(key, "XDG_")
}

// LocalSocketLocator is retained for platform-specific registered adapters;
// the MVP SQLD generic adapter intentionally accepts only TCP HTTP endpoints.
func LocalSocketLocator(path string) LocalEndpoint {
	return LocalEndpoint{Network: "unix", Address: filepath.Clean(path), URL: "", SecurityLevel: SecuritySameUser}
}
