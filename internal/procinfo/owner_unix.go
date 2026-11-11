//go:build unix

package procinfo

import (
	"os"
	"syscall"
)

// FileOwner is the uid that owns the file. It is false when the platform
// gives no owner, so a caller that checks ownership rejects the file.
func FileOwner(fi os.FileInfo) (int, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}
