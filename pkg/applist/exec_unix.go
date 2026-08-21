//go:build unix

package applist

import (
	"io/fs"
	"os"
	"slices"
	"syscall"
)

// executable reports whether the calling process may exec the file.
//
// Testing the whole 0111 mask would offer programs that only root can run, and
// a launcher row that reliably fails is worse than one that is not there. The
// owner and group bits are therefore weighed against this process's own ids.
func executable(info fs.FileInfo) bool {
	mode := info.Mode().Perm()
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// An unfamiliar FileInfo implementation is not worth failing closed
		// over; fall back to "executable by somebody".
		return mode&0o111 != 0
	}

	uid := os.Getuid()
	// Root's exec check is the union of the bits, not the owner's alone.
	if uid == 0 {
		return mode&0o111 != 0
	}
	if uint32(uid) == st.Uid {
		return mode&0o100 != 0
	}
	if uint32(os.Getgid()) == st.Gid {
		return mode&0o010 != 0
	}
	if groups, err := os.Getgroups(); err == nil && slices.Contains(groups, int(st.Gid)) {
		return mode&0o010 != 0
	}
	return mode&0o001 != 0
}

// Argv is the argv that runs e as a pane's own process. On unix the listed
// path is directly executable, so it is the whole command.
func (e Entry) Argv() []string {
	return []string{e.Path}
}
