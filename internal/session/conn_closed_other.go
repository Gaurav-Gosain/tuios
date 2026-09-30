//go:build windows || js

package session

import "net"

// connPeerClosed reports false: this platform has no peek to tell a closed
// connection by, and a wait there ends at its timeout. See
// conn_closed_unix.go.
func connPeerClosed(net.Conn) bool { return false }
