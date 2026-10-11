//go:build !windows

package session

import (
	"os"
	"syscall"
)

// exitSignal is the signal that ended a process, 0 when it exited on its own.
func exitSignal(ps *os.ProcessState) int {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return int(ws.Signal())
	}
	return 0
}
