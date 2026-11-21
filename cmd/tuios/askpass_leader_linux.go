package main

import (
	"os"
	"strconv"
	"strings"
)

// jobLeaderName is the command name of process pid, from the kernel.
func jobLeaderName(pid int) (string, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}
