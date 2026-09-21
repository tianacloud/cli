// Package clientconfig carries immutable, invocation-scoped TLS settings.
package clientconfig

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"

	"github.com/tianacloud/cli/internal/localfile"
)

type Trust struct {
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

// Load adds at most eight bounded deployment CAs to system trust. A supplied
// invalid file never falls back to system roots or insecure verification.
func Load(path string) (*Trust, error) {
	data, err := localfile.Read(path, 64*1024, false)
	if err != nil {
		return nil, errors.New("cannot read CA file: expected a regular, non-symlink file of at most 64 KiB")
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	t := &Trust{Roots: roots}
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
