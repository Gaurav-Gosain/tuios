//go:build !linux && !darwin

package procinfo

import "net"

// PeerPID is 0 where the kernel does not give the peer's pid.
func PeerPID(net.Conn) int { return 0 }

// StartTime cannot be read here.
func StartTime(int) (uint64, bool) { return 0, false }
