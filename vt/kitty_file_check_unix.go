//go:build unix

package vt

import (
	"os"
	"syscall"
)

// kittyOpenRegular opens path the way a check must: O_NONBLOCK, so a FIFO
// swapped in after the path was resolved cannot hold the open, and
// O_NOFOLLOW, so a symlink swapped in leads nowhere. It then asks the open
// file, not the path, whether it is a regular file this user owns.
func kittyOpenRegular(path string) error {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errKittyNotRegular
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return errKittyNotRegular
	}
	return nil
}
