//go:build linux

package terminal

import (
	"fmt"
	"os"
)

// shellCWD reads the directory out of procfs, where the kernel publishes it as
// a symlink the owner can read.
func shellCWD(pgid int) (string, bool) {
	cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pgid))
	if err != nil || cwd == "" {
		return "", false
	}
	return cwd, true
}
