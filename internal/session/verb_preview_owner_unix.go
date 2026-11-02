//go:build !windows

package session

import (
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// previewFileOwner is the names of a file's owner and group, or their numbers.
func previewFileOwner(fi os.FileInfo) (owner, group string) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "", ""
	}
	owner, group = strconv.Itoa(int(st.Uid)), strconv.Itoa(int(st.Gid))
	if u, err := user.LookupId(owner); err == nil {
		owner = u.Username
	}
	if g, err := user.LookupGroupId(group); err == nil {
		group = g.Name
	}
	return owner, group
}
