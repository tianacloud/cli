package gitremote

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/tianacloud/cli/internal/authclient"

	"github.com/tianacloud/cli/internal/clientconfig"
	"github.com/tianacloud/cli/internal/supervisor"
)

type config struct {
	address string
	tls     *tls.Config
	token   *supervisor.SecretToken
}

func configuration(repo supervisor.Endpoint) (config, error) {
	return configurationWithTrust(repo, nil)
}

func configurationWithTrust(repo supervisor.Endpoint, trust *clientconfig.Trust) (config, error) {
	c := config{address: net.JoinHostPort(repo.Hostname(), repo.Port()), tls: &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, NextProtos: []string{"h2"}, ServerName: repo.Hostname()}}
	if address, ok := os.LookupEnv("TIANA_GATEWAY_ADDRESS"); ok {
		host, port, err := net.SplitHostPort(address)
		n, e := strconv.Atoi(port)
		if err != nil || e != nil || host == "" || n < 1 || n > 65535 || strconv.Itoa(n) != port || strings.ContainsAny(host, " /?#@") {
			return c, errors.New("invalid Gateway address")
		}
		c.address = address
	}
	if trust != nil {
		c.tls.RootCAs = trust.Roots
	} else if path, ok := os.LookupEnv("TIANA_CA_FILE"); ok {
		data, err := readRegular(path, 65536, false)
		if err != nil {
			return c, errors.New("cannot read bounded deployment CA file")
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		count := 0
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
			if err != nil {
				return c, errors.New("invalid deployment CA certificate")
			}
			roots.AddCert(cert)
			count++
		}
		if count < 1 || count > 8 {
			return c, errors.New("CA file must contain 1..8 certificates")
		}
		c.tls.RootCAs = roots
	}
	value, err := authclient.ConnectionCredential(context.Background())
	if err != nil {
		return c, err
	}
	defer clear(value)
	token, err := supervisor.ParseToken(value)
	if err != nil {
		return c, errors.New("invalid connection credential")
	}
	c.token = token
	return c, nil
}

func readBounded(file *os.File, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(file, limit+1))
}
