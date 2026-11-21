//go:build (unix || linux || freebsd || openbsd || netbsd) && !darwin

package terminal

import (
	"fmt"
	"os"
	"strings"
)

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
