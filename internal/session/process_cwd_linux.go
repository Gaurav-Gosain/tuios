//go:build linux

package session

import (
	"fmt"
	"os"
)

// processCwd reads the directory out of procfs, where the kernel publishes it
// as a symlink the owner can read.
func processCwd(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil || cwd == "" {
		return "", false
	}
	return cwd, true
}
