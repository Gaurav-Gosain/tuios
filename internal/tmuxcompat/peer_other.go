//go:build !linux && !darwin

package tmuxcompat

import "net"

// peerPID is 0 where the kernel does not give the peer's pid. The holder then
// cannot ask the daemon about the caller, and respawn-pane's own check in the
// shim is the only one.
func peerPID(net.Conn) int { return 0 }
