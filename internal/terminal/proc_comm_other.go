//go:build (unix || linux || freebsd || openbsd || netbsd) && !darwin

package terminal

import (
	"fmt"
	"os"
	"strings"
)

// procComm returns the command name of a process, or "" when it cannot be read.
// It reads /proc, so it answers on Linux and on the BSDs that mount a Linux
// compatible procfs, and is empty elsewhere.
func procComm(pid int) string {
	if pid <= 0 {
		return ""
	}
	comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(comm))
}
