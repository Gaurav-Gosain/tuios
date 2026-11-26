package session

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

// zombie reports whether pid has exited and waits to be reaped.
func zombie(pid int) bool {
	stat, ok := procinfo.Stat(pid)
	if !ok {
		return false
	}
	// The state is the first field after the name, which ends at the last
	// closing parenthesis.
	i := strings.LastIndexByte(stat, ')')
	return i >= 0 && i+2 < len(stat) && stat[i+2] == 'Z'
}
