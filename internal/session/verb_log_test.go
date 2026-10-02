package session

import (
	"strings"
)

// ringHas reports whether any entry in the log ring contains sub.
func ringHas(sub string) bool {
	for _, e := range GetLogEntries(0) {
		if strings.Contains(e.Message, sub) {
			return true
		}
	}
	return false
}

// ringDump renders the ring for a failure message.
func ringDump() string {
	var b strings.Builder
	for _, e := range GetLogEntries(0) {
		b.WriteString("  [" + e.Level + "] " + e.Message + "\n")
	}
	return b.String()
}
