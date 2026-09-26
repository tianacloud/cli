//go:build !darwin

package apppublish

import (
	"context"
	"net"
	"time"
)

var directUploadDialer = net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}

func directUploadDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return directUploadDialer.DialContext(ctx, network, address)
}
