//go:build !windows && !js

package session

import (
	"os"
	"syscall"
)

// fileIDOf is the device and inode of a stat result.
func fileIDOf(info os.FileInfo) fileID {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return fileID{dev: uint64(st.Dev), ino: st.Ino} //nolint:gosec,unconvert // the field's type differs by platform
	}
	return fileID{}
}
