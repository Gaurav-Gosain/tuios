//go:build !windows && !js

package session

import (
	"os"
	"syscall"
)

// fileIDOf is the device, inode, size and times of a stat result.
func fileIDOf(info os.FileInfo) fileID {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return fileID{
			dev: uint64(st.Dev), ino: st.Ino, //nolint:gosec,unconvert // the field's type differs by platform
			size: info.Size(), mtime: info.ModTime().UnixNano(), ctime: changeTime(st),
		}
	}
	return fileID{}
}

// fileUID is the user that owns a stat result's file.
func fileUID(info os.FileInfo) (int, bool) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid), true
	}
	return 0, false
}
