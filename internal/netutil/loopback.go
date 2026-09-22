// Package netutil holds the small network helpers the servers share.
package netutil

import "net"

// IsLoopbackHost reports whether a bind address keeps traffic inside this
// machine.
//
// Both servers gate on this: tuios-web refuses a non-loopback bind in clear
// text, and the SSH server refuses one with no authentication. They share this
// one function because "is this address on the network" is the one question
// they must agree on.
func IsLoopbackHost(host string) bool {
	if host == "" || host == "localhost" {
		return true
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
