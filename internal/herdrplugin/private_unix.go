//go:build unix

package herdrplugin

import (
	"os"
	"syscall"
)

func privateToMe(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0o022 != 0 {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
