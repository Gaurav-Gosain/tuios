//go:build linux

package session

import (
	"os"
	"strconv"
)

func fdPath(f *os.File) (string, bool) {
	p, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(int(f.Fd()))) //nolint:gosec // a file descriptor is small
	if err != nil || len(p) == 0 || p[0] != '/' {
		return "", false
	}
	return p, true
}
