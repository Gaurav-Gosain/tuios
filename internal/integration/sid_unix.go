//go:build unix

package integration

import "golang.org/x/sys/unix"

func selfSID() int {
	sid, err := unix.Getsid(0)
	if err != nil {
		return 0
	}
	return sid
}
