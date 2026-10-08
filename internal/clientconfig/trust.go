// Package clientconfig carries immutable, invocation-scoped TLS settings.
package clientconfig

import (
	"context"
	"crypto/x509"
	_ "embed"
	"encoding/pem"
	"errors"
	"os"
	"strings"

	"github.com/tianacloud/cli/internal/localfile"
)

//go:embed staging-root.crt
var stagingCA []byte

type Trust struct {
	IsDefault    bool
	Roots        *x509.CertPool
	Certificates [][]byte
}
type trustKey struct{}

func WithTrust(ctx context.Context, trust *Trust) context.Context {
	return context.WithValue(ctx, trustKey{}, trust)
}
func FromContext(ctx context.Context) *Trust {
	trust, _ := ctx.Value(trustKey{}).(*Trust)
	return trust
}

// Load adds at most eight bounded deployment CAs to system trust.
// With no explicit file, only the staging management origin adds its bundled CA.
// Other origins use system trust. An invalid explicit file is an error.
func Load(path string) (*Trust, error) {
	var data []byte
	if strings.TrimSuffix(strings.TrimSpace(os.Getenv("TIANA_API_ORIGIN")), "/") == StagingManagementOrigin {
		data = stagingCA
	}
	if path != "" {
		var err error
		data, err = localfile.Read(path, 64*1024, false)
		if err != nil {
			return nil, errors.New("cannot read CA file: expected a regular, non-symlink file of at most 64 KiB")
		}
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	t := &Trust{Roots: roots, IsDefault: path == ""}
	if path == "" && len(data) == 0 {
		return t, nil
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
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || len(block.Bytes) > 16*1024 {
			return nil, errors.New("invalid CA certificate")
		}
		t.Certificates = append(t.Certificates, cert.Raw)
		if len(t.Certificates) > 8 {
			return nil, errors.New("CA file must contain 1..8 certificates")
		}
		roots.AddCert(cert)
	}
	if len(t.Certificates) == 0 {
		return nil, errors.New("CA file must contain 1..8 certificates")
	}
	return t, nil
}
