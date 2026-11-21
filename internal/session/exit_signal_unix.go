//go:build !windows

package session

import (
	"os"
	"syscall"
)

func exitSignal(ps *os.ProcessState) int {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return int(ws.Signal())
	}
	return 0
}
