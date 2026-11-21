package session

import "golang.org/x/sys/unix"

func pendingBytes(fd int) int {
	n, err := unix.IoctlGetInt(fd, unix.TIOCINQ)
	if err != nil {
		return -1
	}
	return n
}
