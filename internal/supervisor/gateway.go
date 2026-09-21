package supervisor

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/tianacloud/cli/internal/buildconfig"
)

type gatewayTrust struct {
	RootCertDER    [][]byte
	Address        string
	UseWebPKIRoots bool
	InsecureTLS    bool
}

// gatewayInsecureTLS combines the explicit --insecure selection with the
// build-wide setting. A build that enables insecure TLS makes it the default
// without exposing the debug-only flag.
func gatewayInsecureTLS(options GatewayOptions) bool {
	return options.InsecureTLS || buildconfig.TLSInsecure()
}

func resolveGatewayTrust(options GatewayOptions) (gatewayTrust, error) {
	if err := validateGatewayAddress(options.Address); err != nil {
		return gatewayTrust{}, err
	}
	if options.InsecureTLS && !debugTLSAvailable && !buildconfig.TLSInsecure() {
		return gatewayTrust{}, fmt.Errorf("%w: insecure TLS is unavailable in release builds", ErrGatewayConfiguration)
	}
	insecure := gatewayInsecureTLS(options)
	trust := gatewayTrust{Address: options.Address, UseWebPKIRoots: !insecure, InsecureTLS: insecure}
	if len(options.CACertificates) > 0 {
		if len(options.CACertificates) > GatewayRootCertCount {
			return gatewayTrust{}, ErrGatewayConfiguration
		}
		for _, der := range options.CACertificates {
			if len(der) > GatewayRootCertLimit {
				return gatewayTrust{}, ErrGatewayConfiguration
			}
			if _, err := x509.ParseCertificate(der); err != nil {
				return gatewayTrust{}, ErrGatewayConfiguration
			}
		}
		trust.RootCertDER = options.CACertificates
		trust.InsecureTLS = false
		trust.UseWebPKIRoots = true
	} else if options.CAFile != "" {
		data, err := os.ReadFile(options.CAFile)
		if err != nil {
			return gatewayTrust{}, fmt.Errorf("read deployment CA: %w", err)
		}
		for len(data) > 0 {
			block, rest := pem.Decode(data)
			if block == nil {
				break
			}
			data = rest
			if block.Type != "CERTIFICATE" {
				continue
			}
			if _, err := x509.ParseCertificate(block.Bytes); err != nil {
				return gatewayTrust{}, fmt.Errorf("parse deployment CA: %w", err)
			}
			trust.RootCertDER = append(trust.RootCertDER, block.Bytes)
		}
		if len(trust.RootCertDER) == 0 {
			return gatewayTrust{}, fmt.Errorf("deployment CA file contains no PEM certificate")
		}
		trust.InsecureTLS = false
		trust.UseWebPKIRoots = true
	}
	return trust, nil
}

func resolveGatewayDialTarget(options GatewayOptions, endpoint Endpoint) (GatewayOptions, error) {
	if options.Address == "" {
		options.Address = net.JoinHostPort(endpoint.Hostname(), endpoint.Port())
	}
	return options, nil
}

func validateGatewayAddress(value string) error {
	if value == "" {
		return nil
	}
	if strings.TrimSpace(value) != value || strings.Contains(value, "://") {
		return ErrGatewayConfiguration
	}
	host, portText, err := net.SplitHostPort(value)
	if err != nil || host == "" || portText == "" {
		return fmt.Errorf("%w: gateway-address must be host:port", ErrGatewayConfiguration)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return fmt.Errorf("%w: gateway-address port is invalid", ErrGatewayConfiguration)
	}
	if net.ParseIP(host) == nil {
		if len(host) > 253 || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
			return fmt.Errorf("%w: gateway-address host is invalid", ErrGatewayConfiguration)
		}
		for _, label := range strings.Split(host, ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return fmt.Errorf("%w: gateway-address host is invalid", ErrGatewayConfiguration)
			}
			for _, character := range label {
				if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-') {
					return fmt.Errorf("%w: gateway-address host is invalid", ErrGatewayConfiguration)
				}
			}
		}
	}
	return nil
}
