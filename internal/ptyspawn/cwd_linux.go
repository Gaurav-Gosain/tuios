//go:build linux

package ptyspawn

import (
	"fmt"
	"os"
)

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
