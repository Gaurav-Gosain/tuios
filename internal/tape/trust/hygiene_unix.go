//go:build !windows

package trust

import (
	"fmt"
	"os"
	"syscall"
)

// ownedByCurrentUser reports whether info's owner is the current user. A tape
// (or the trust store) owned by anyone else is not something we can vouch for.
func ownedByCurrentUser(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return int(st.Uid) == os.Getuid()
}

// isGroupOrWorldWritable reports whether info is writable by group or other. A
// writable tape can be edited by someone other than its owner after approval.
func isGroupOrWorldWritable(info os.FileInfo) bool {
	return info.Mode().Perm()&0o022 != 0
}

// isGroupOrWorldAccessible reports whether info grants any permission bit to
// group or other. Used for the trust store, which must be 0600.
func isGroupOrWorldAccessible(info os.FileInfo) bool {
	return info.Mode().Perm()&0o077 != 0
}

// hygieneReason applies the tape eligibility preconditions to info and returns
// (reason, ok). When ok is false, reason explains why the tape is ineligible.
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
		return fmt.Sprintf("larger than %d KiB", MaxTapeSize/1024), false
	}
	return "", true
}
