//go:build unix

package session

import (
	"errors"
	"syscall"
	"time"
)

// askpassPoll is how often a waiting prompt looks at its program where the
// kernel gives no way to wait for it.
var askpassPoll = 2 * time.Second

// pollExit closes gone once pid no longer runs, looking every askpassPoll,
// and gives up when stop closes.
func pollExit(pid int, stop <-chan struct{}, gone chan<- struct{}) {
	t := time.NewTicker(askpassPoll)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
				close(gone)
				return
			}
		}
	}
}
