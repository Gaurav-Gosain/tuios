//go:build !windows && !js && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd

package session

import "golang.org/x/sys/unix"

// newGateWaiter returns a read gate's wait: a poll of the master and the wake
// pipe. On Linux poll keeps nothing between calls, and costs a flood about a
// third of a millisecond of CPU per MB (docs/design/pane-read-path.md).
func newGateWaiter(_, _ int) (func(master, wake int) (bool, error), func(), error) {
	return pollGate, func() {}, nil
}

func pollGate(master, wake int) (bool, error) {
	fds := []unix.PollFd{
		{Fd: int32(master), Events: unix.POLLIN}, //nolint:gosec // a descriptor number
		{Fd: int32(wake), Events: unix.POLLIN},   //nolint:gosec
	}
	if _, err := unix.Poll(fds, -1); err != nil {
		return false, err
	}
	return fds[1].Revents != 0, nil
}
