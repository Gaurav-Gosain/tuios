//go:build !windows

package trust

import (
	"os"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/units"
)

func ownedByCurrentUser(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return int(st.Uid) == os.Getuid()
}

func isGroupOrWorldWritable(info os.FileInfo) bool {
	return info.Mode().Perm()&0o022 != 0
}

func isGroupOrWorldAccessible(info os.FileInfo) bool {
	return info.Mode().Perm()&0o077 != 0
}

func hygieneReason(info os.FileInfo) (string, bool) {
	if !info.Mode().IsRegular() {
		return "not a regular file", false
	}
	if !ownedByCurrentUser(info) {
		return "not owned by you", false
	}
	if isGroupOrWorldWritable(info) {
		return "group- or world-writable", false
	}
	if info.Size() > MaxTapeSize {
		return "larger than " + units.Bytes(int64(MaxTapeSize)), false
	}
	return "", true
}
