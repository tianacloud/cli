package gitremote

import (
	"context"
	"crypto/tls"
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

func configurationWithContext(ctx context.Context, repo supervisor.Endpoint) (config, error) {
	c := config{address: net.JoinHostPort(repo.Hostname(), repo.Port()), tls: &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, NextProtos: []string{"h2"}, ServerName: repo.Hostname()}}
	if address, ok := os.LookupEnv("TIANA_GATEWAY_ADDRESS"); ok {
		host, port, err := net.SplitHostPort(address)
		n, e := strconv.Atoi(port)
		if err != nil || e != nil || host == "" || n < 1 || n > 65535 || strconv.Itoa(n) != port || strings.ContainsAny(host, " /?#@") {
			return c, errors.New("invalid Gateway address")
		}
		c.address = address
	}
	if trust := clientconfig.FromContext(ctx); trust != nil {
		c.tls.RootCAs = trust.Roots
	} else {
		trust, err := clientconfig.Load(os.Getenv("TIANA_CA_FILE"))
		if err != nil {
			return c, err
		}
		c.tls.RootCAs = trust.Roots
	}

	value, err := authclient.ConnectionCredential(ctx)
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
