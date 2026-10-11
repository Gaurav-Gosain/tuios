//go:build linux || darwin

package session

import "golang.org/x/sys/unix"

// diskFree is the bytes an unprivileged write may still use on the disk that
// holds dir, or -1 when it cannot be read.
func diskFree(dir string) int64 {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return -1
	}
	return int64(st.Bavail) * int64(st.Bsize) //nolint:gosec,unconvert // block counts and sizes are far below int64's range
}
