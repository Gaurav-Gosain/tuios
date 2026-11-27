//go:build !darwin && !freebsd && !netbsd && !openbsd && !dragonfly && !windows

package pidwait

import (
	"context"
	"time"
)

// pollInterval is how often a platform with no exit notice checks.
const pollInterval = time.Second

// pollWait checks alive once a second until it is false or ctx ends.
func pollWait(ctx context.Context, pid int, alive func(int) bool) error {
	if !alive(pid) {
		return ErrNotRunning
	}
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if !alive(pid) {
				return nil
			}
		}
	}
}
