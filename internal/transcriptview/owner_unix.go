//go:build unix

package transcriptview

import (
	"os"
	"syscall"
)

// serviceFileSafe reports whether service.json belongs to this user and no
// one else can write it: whoever can write it chooses where the password
// goes.
func serviceFileSafe(_ *os.File, info os.FileInfo) bool {
	return info.Mode().Perm()&0o022 == 0 && fileOwner(info) == os.Getuid()
}

// fileOwner is the uid that owns the file, -2 when it cannot be read.
func fileOwner(info os.FileInfo) int {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid)
	}
	return -2
}

// myUID is the user a connection's server side must belong to. A test
// changes it to see the check refuse.
var myUID = os.Getuid
