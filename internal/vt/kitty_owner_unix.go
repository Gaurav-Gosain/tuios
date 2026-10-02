//go:build unix

package vt

import (
	"os"
	"syscall"
)

// kittyUID is this process's user id. It is a variable so a test can play a
// different user: a file owned by another user takes root to make.
var kittyUID = os.Getuid

// kittyOwnedByMe reports whether this process's user owns the file.
func kittyOwnedByMe(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == kittyUID()
}
