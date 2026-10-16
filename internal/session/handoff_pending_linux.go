package session

import "golang.org/x/sys/unix"

// pendingBytes is how many bytes the kernel holds for a reader of fd, or -1
// when that cannot be read.
func pendingBytes(fd int) int {
	n, err := unix.IoctlGetInt(fd, unix.TIOCINQ)
	if err != nil {
		return -1
	}
	return n
}
