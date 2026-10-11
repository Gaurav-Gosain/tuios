//go:build unix

package session

import (
	"os"
	"strconv"
	"syscall"
)

// fsKey names the filesystem that holds dir, so writes to one disk are
// counted together. It is dir itself when the system cannot say.
func fsKey(dir string) string {
	fi, err := os.Stat(dir)
	if err != nil {
		return dir
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return dir
	}
	return "dev:" + strconv.FormatUint(uint64(st.Dev), 10) //nolint:gosec,unconvert // Dev is int32 or uint64 by system
}
