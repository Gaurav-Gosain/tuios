// Package netutil holds the small network helpers the servers share.
package netutil

import (
	"net"
	"net/netip"
	"strings"
)

// IsLoopbackHost reports whether a bind address keeps traffic inside this
// machine.
//
// Both servers gate on this: tuios-web refuses a non-loopback bind in clear
// text, and the SSH server refuses one with no authentication. They share this
// one function because "is this address on the network" is the one question
// they must agree on.
//
// An empty host is not loopback. The servers join it with the port into
// ":port", which listens on every interface.
func IsLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	addr, ok := HostAddr(host)
	return ok && addr.IsLoopback()
}

// IsUnspecifiedHost reports whether a bind address is the unspecified
// address, such as 0.0.0.0 or ::, which listens on every interface, loopback
// included.
func IsUnspecifiedHost(host string) bool {
	addr, ok := HostAddr(host)
	return ok && addr.IsUnspecified()
}

// HostAddr parses a bind host as an IP address. It takes an optional port,
// brackets and a zone, so "[::1%lo]:80" gives ::1. The zone is dropped:
// net.Listen accepts "::%lo" and binds every interface, so a check that
// cannot parse the zone must not read the address as a host name. An
// IPv4-mapped IPv6 address is unmapped. ok is false for a host name.
func HostAddr(host string) (addr netip.Addr, ok bool) {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.WithZone("").Unmap(), true
}
