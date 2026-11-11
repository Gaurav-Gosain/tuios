//go:build unix

package integration

import "golang.org/x/sys/unix"

// selfSID is this process's session id, 0 when it cannot be read.
func selfSID() int {
	sid, err := unix.Getsid(0)
	if err != nil {
		return 0
	}
	return sid
}
