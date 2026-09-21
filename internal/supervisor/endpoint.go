package supervisor

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

const endpointIDLen = 29

// Endpoint carries the deployed hostname and TCP port of a public database.
type Endpoint struct {
	id       string
	hostname string
	port     string
}

func ParseEndpoint(value string) (Endpoint, error) {
	id, suffix, ok := strings.Cut(value, ".")
	if !ok || suffix == "" || !validEndpointID(id) || value != strings.ToLower(value) {
		return Endpoint{}, ErrInvalidEndpoint
	}
	if err := validateGatewayAddress(net.JoinHostPort(value, "443")); err != nil {
		return Endpoint{}, ErrInvalidEndpoint
	}
	return Endpoint{id: id, hostname: value, port: "443"}, nil
}

// ParseEndpointURL retains the published physical port; tunnel identity uses
// the hostname independently of the TCP listener's port.
func ParseEndpointURL(value string) (Endpoint, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return Endpoint{}, ErrInvalidEndpoint
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Opaque != "" || parsed.User != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawPath != "" || parsed.ForceQuery || parsed.RawQuery != "" || parsed.Fragment != "" {
		return Endpoint{}, ErrInvalidEndpoint
	}
	endpoint, err := ParseEndpoint(parsed.Hostname())
	if err != nil {
		return Endpoint{}, err
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.ParseUint(port, 10, 16)
		if err != nil || number == 0 || strconv.FormatUint(number, 10) != port {
			return Endpoint{}, ErrInvalidEndpoint
		}
		endpoint.port = port
	}
	authority := endpoint.hostname
	if parsed.Port() != "" {
		authority = net.JoinHostPort(endpoint.hostname, endpoint.port)
	}
	if parsed.Host != authority {
		return Endpoint{}, ErrInvalidEndpoint
	}
	return endpoint, nil
}

func (e Endpoint) URL() string {
	authority := e.hostname
	if e.port != "443" {
		authority = net.JoinHostPort(e.hostname, e.port)
	}
	return "https://" + authority
}

func validEndpointID(value string) bool {
	if len(value) != endpointIDLen || !strings.HasPrefix(value, "ep-") {
		return false
	}
	if value[3] < '0' || value[3] > '7' {
		return false
	}
	for i := 4; i < len(value); i++ {
		if !validCrockfordLower(value[i]) {
			return false
		}
	}
	return true
}

func validCrockfordLower(value byte) bool {
	return (value >= '0' && value <= '9') ||
		(value >= 'a' && value <= 'h') || value == 'j' || value == 'k' ||
		value == 'm' || value == 'n' || (value >= 'p' && value <= 't') ||
		(value >= 'v' && value <= 'z')
}

func (e Endpoint) ID() string       { return e.id }
func (e Endpoint) Hostname() string { return e.hostname }
func (e Endpoint) String() string   { return e.hostname }

func (e Endpoint) Port() string { return e.port }
