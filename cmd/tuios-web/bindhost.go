package main

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/netutil"
)

var webBindAddr string

type hostResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

func resolveBindHost(ctx context.Context, r hostResolver, host string) (string, error) {
	if host == "" {
		return "", nil
	}
	if _, port, err := net.SplitHostPort(host); err == nil {
		return "", fmt.Errorf("cannot use --host %s, because it has a port (%s). Give the port with --port", host, port)
	}
	if addr, ok := netutil.HostAddr(host); ok {
		if addr.IsLoopback() || addr.IsUnspecified() {
			return addr.String(), nil
		}
		return host, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	found, err := r.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return "", fmt.Errorf("cannot find the address of --host %s: %w. Give an IP address, such as 127.0.0.1", host, err)
	}
	return pickBindAddr(host, found)
}

func bindKind(a netip.Addr) string {
	switch {
	case a.IsUnspecified():
		return "every interface"
	case a.IsLoopback():
		return "loopback"
	}
	return "network"
}

func pickBindAddr(host string, found []netip.Addr) (string, error) {
	if len(found) == 0 {
		return "", fmt.Errorf("--host %s has no address. Give an IP address, such as 127.0.0.1", host)
	}
	addrs := make([]netip.Addr, len(found))
	for i, a := range found {
		addrs[i] = a.WithZone("").Unmap()
	}
	kind := bindKind(addrs[0])
	for _, a := range addrs[1:] {
		if bindKind(a) != kind {
			list := make([]string, len(addrs))
			for i, a := range addrs {
				list[i] = a.String()
			}
			return "", fmt.Errorf("the host %s has both loopback and other addresses (%s). Give one IP address with --host",
				host, strings.Join(list, ", "))
		}
	}
	pick := addrs[0]
	for _, a := range addrs {
		if a.Is4() {
			pick = a
			break
		}
	}
	if pick.Is6() && pick.IsLinkLocalUnicast() {
		return "", fmt.Errorf("--host %s is a link-local address (%s). Give the address with its zone, such as fe80::1%%eth0", host, pick)
	}
	return pick.String(), nil
}

var netResolver hostResolver = net.DefaultResolver
