//go:build darwin

package apppublish

import (
	"context"
	"errors"
	"net"
	"sort"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

var directDialer = net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}

func directUploadDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(address)
	if err == nil {
		if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil && ip.IsLoopback() {
			return directDialer.DialContext(ctx, network, address)
		}
	}

	interfaces, err := uploadInterfaces()
	if err != nil || len(interfaces) == 0 {
		return directDialer.DialContext(ctx, network, address)
	}
	var failures []error
	for _, nic := range interfaces {
		dialer := directDialer
		dialer.Control = bindUploadInterface(nic.Index)
		conn, dialErr := dialer.DialContext(ctx, network, address)
		if dialErr == nil {
			return conn, nil
		}
		failures = append(failures, dialErr)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, errors.Join(failures...)
}

func uploadInterfaces() ([]net.Interface, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	eligible := interfaces[:0]
	for _, nic := range interfaces {
		if nic.Flags&net.FlagUp == 0 || nic.Flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
			continue
		}
		addresses, addrErr := nic.Addrs()
		if addrErr != nil {
			continue
		}
		for _, address := range addresses {
			ip, _, parseErr := net.ParseCIDR(address.String())
			if parseErr == nil && ip.IsGlobalUnicast() {
				eligible = append(eligible, nic)
				break
			}
		}
	}
	sort.SliceStable(eligible, func(i, j int) bool { return eligible[i].Index < eligible[j].Index })
	return eligible, nil
}

func bindUploadInterface(index int) func(string, string, syscall.RawConn) error {
	return func(network, _ string, raw syscall.RawConn) error {
		var bindErr error
		controlErr := raw.Control(func(fd uintptr) {
			if strings.HasSuffix(network, "6") {
				bindErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF, index)
				return
			}
			bindErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF, index)
		})
		if controlErr != nil {
			return controlErr
		}
		return bindErr
	}
}
