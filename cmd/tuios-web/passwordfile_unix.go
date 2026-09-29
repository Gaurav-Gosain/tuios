//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// checkPasswordFileMode refuses a password file that another user owns, or
// that anyone other than its owner can read or write.
func checkPasswordFileMode(path string, info os.FileInfo, uid int) error {
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != uid {
		return fmt.Errorf("the password file %s belongs to another user. Use a file that you own", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("other users can read the password file %s. Run 'chmod 600 %s', then start tuios-web again", path, path)
	}
	return nil
}
