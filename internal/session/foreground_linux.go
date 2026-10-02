//go:build linux

package session

import (
	"os"
	"strconv"
)

// readForegroundPGID reads field 8 (tpgid) of /proc/<pid>/stat, the foreground
// process group id of the process's controlling terminal.
func readForegroundPGID(pid int) (int, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	return parseStatTPGID(string(data))
}

// parseStatTPGID extracts the tpgid (foreground process group id, field 8) from
// the contents of a /proc/<pid>/stat line.
func parseStatTPGID(s string) (int, bool) { return parseStatField(s, 8) }
