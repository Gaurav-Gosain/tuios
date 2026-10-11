//go:build linux

package session

import (
	"os"
	"strconv"
)

// fdPath is the path the system has for the open file, with every link
// resolved, as it is now: a rename after the open shows here. ok is false
// when the system cannot say.
func fdPath(f *os.File) (string, bool) {
	p, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(int(f.Fd()))) //nolint:gosec // a file descriptor is small
	if err != nil || len(p) == 0 || p[0] != '/' {
		return "", false
	}
	return p, true
}
