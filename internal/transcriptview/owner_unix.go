//go:build unix

package transcriptview

import (
	"os"
	"syscall"
)

// fileOwner is the uid that owns the file, -2 when it cannot be read.
func fileOwner(info os.FileInfo) int {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid)
	}
	return -2
}
