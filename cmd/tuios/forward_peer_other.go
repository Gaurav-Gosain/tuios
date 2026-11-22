//go:build !linux

package main

import "net"

// forwardPeerIsUs has no cheap check outside Linux, so every connection to
// the loopback is accepted, as with ssh -L.
func forwardPeerIsUs(net.Conn) bool { return true }
