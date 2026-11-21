//go:build unix

package vt

import (
	"os"
	"syscall"
)

var kittyUID = os.Getuid

func kittyOwnedByMe(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == kittyUID()
}
