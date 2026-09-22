//go:build linux

package ptyspawn

import (
	"fmt"
	"os"
)

// ProcessCwd reads the directory out of procfs, where the kernel publishes it
// as a symlink the owner can read.
func ProcessCwd(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil || cwd == "" {
		return "", false
	}
	return cwd, true
}
