//go:build !windows

package main

import (
	"os"
	"syscall"
)

// fileOwner is the user id that owns the file, for the StrictModes check of
// tuios pair.
func fileOwner(fi os.FileInfo) (uint32, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Uid, true
}
