//go:build !windows

package client

import (
	"os"
	"syscall"
)

// ownedByUser reports a file this user owns.
func ownedByUser(st os.FileInfo) bool {
	s, ok := st.Sys().(*syscall.Stat_t)
	return ok && int(s.Uid) == os.Getuid()
}
