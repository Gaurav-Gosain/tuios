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

// The bind address.
//
// The access rules ask two questions of --host: does it listen on every
// interface, and does it stay on this machine. sip asks the second one too,
// to decide whether to check the Host header. If those questions are asked of
// the string while the listener resolves it, they can disagree:
//
//   - A cgo build resolves through getaddrinfo, which reads inet_aton forms.
//     "0" and "0x0" bind every interface, and "127.1" and "0177.0.0.1" bind
//     127.0.0.1. None of them parses as an IP address.
//   - Any build resolves names: "LOCALHOST", "ip6-localhost" and the
//     hostname line of /etc/hosts bind loopback, and are not "localhost".
//
// So --host is resolved once, with the resolver the listener uses, and the
// one address that comes back is what the checks read and what sip binds.
// For a loopback bind sip then sees 127.0.0.1 or ::1 and checks the Host
// header. webHost keeps the spelling the user typed, for the URL, the
// messages and the certificate names.

// webBindAddr is --host resolved to the address the server listens on. It is
// set by runWebServer before any check runs.
var webBindAddr string

// hostResolver is the part of net.Resolver that resolveBindHost needs, so a
// test can give it the answers of a cgo build.
type hostResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// resolveBindHost turns --host into one IP address.
//
// An IP address is used as it is, with the zone dropped from a loopback or
// an unspecified address, where it changes nothing. A zone on any other
// address stays: it picks the interface (fe80::1%eth0). The empty host stays
// empty: sip binds it to localhost and checks the Host header for it.
//
// A name is resolved. Its addresses must all be of one kind: all
// unspecified, all loopback, or all neither. A mix is refused, because the
// address the listener picks would decide who can connect.
func resolveBindHost(ctx context.Context, r hostResolver, host string) (string, error) {
	if host == "" {
		return "", nil
	}
	// A port in --host was dropped from a loopback or unspecified address
	// and kept on any other one, where sip v0.9.4 refuses it only after the
	// password is printed. Refuse it here, before anything starts.
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

// bindKind sorts an address by who can reach it.
func bindKind(a netip.Addr) string {
	switch {
	case a.IsUnspecified():
		return "every interface"
	case a.IsLoopback():
		return "loopback"
	}
	return "network"
}

// pickBindAddr picks the address the listener would bind from the addresses
// a name resolved to. Like net.Listen, it takes the first IPv4 address, or
// the first address when there is none.
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
	// A link-local address needs a zone, and a name gives none. Handing the
	// name on would make the listener resolve it a second time, after the
	// checks, so refuse it and ask for the address with its zone.
	if pick.Is6() && pick.IsLinkLocalUnicast() {
		return "", fmt.Errorf("--host %s is a link-local address (%s). Give the address with its zone, such as fe80::1%%eth0", host, pick)
	}
	return pick.String(), nil
}

// netResolver is the resolver net.Listen uses.
var netResolver hostResolver = net.DefaultResolver
