//go:build !linux && !darwin

package procinfo

import (
	"net"
	"time"
)

// PeerPID is 0 where the kernel does not give the peer's pid.
func PeerPID(net.Conn) int { return 0 }

// StartTime cannot be read here.
func StartTime(int) (uint64, bool) { return 0, false }

// ParentPID cannot be read here.
func ParentPID(int) (int, bool) { return 0, false }

// EnvVar cannot be read here.
func EnvVar(int, string) (string, bool) { return "", false }

// StartedAt cannot be read here.
func StartedAt(int) (time.Time, bool) { return time.Time{}, false }
