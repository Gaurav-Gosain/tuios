//go:build !windows && !js && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd

package session

import "golang.org/x/sys/unix"

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
