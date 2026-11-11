//go:build linux

package integration

import (
	"os"
	"strconv"
	"strings"
)

// processName reads /proc/<pid>/comm, empty when it cannot be read.
func processName(pid int) string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
