//go:build !slim

package session

import (
	"strings"
)

// agentHint returns the harness a process's TUIOS_AGENT names, trimmed and
// lowercased, or "" when it names none.
func agentHint(read func(pid int) (string, bool), pid int) string {
	if read == nil || pid <= 1 {
		return ""
	}
	v, ok := read(pid)
	if !ok {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(v))
}
