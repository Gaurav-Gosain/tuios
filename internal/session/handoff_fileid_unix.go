//go:build !windows && !js

package session

import (
	"os"
	"syscall"
)

func fileIDOf(info os.FileInfo) fileID {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return fileID{
			dev: uint64(st.Dev), ino: st.Ino, //nolint:gosec,unconvert // the field's type differs by platform
			size: info.Size(), mtime: info.ModTime().UnixNano(), ctime: changeTime(st),
		}
	}
	return fileID{}
}

func fileUID(info os.FileInfo) (int, bool) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid), true
	}
	return 0, false
}
