//go:build unix

package session

import (
	"os"
	"syscall"
)

// ownedByDaemonUser reports whether the folder at dir belongs to the user the
// daemon runs as. A files folder the user does not own, such as /etc or
// /usr, is the system's, and the files grant never reaches it.
func ownedByDaemonUser(dir string) bool {
	fi, err := os.Stat(dir)
	if err != nil {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return int(st.Uid) == os.Getuid()
}

// fileOwnedByDaemonUser reports whether the file fi describes belongs to the
// user the daemon runs as.
func fileOwnedByDaemonUser(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
