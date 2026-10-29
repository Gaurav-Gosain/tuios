package app

import (
	"os"
	"runtime"
	"strings"
	"sync"
)

// localHostName is this machine's host name up to the first dot, the name
// a person knows it by, or "" when it cannot be read. The browser build has
// no host name of its own.
var localHostName = sync.OnceValue(func() string {
	if runtime.GOOS == "js" {
		return ""
	}
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	h, _, _ = strings.Cut(h, ".")
	return printableTitle(strings.TrimSpace(h))
})

// thisMachineLabel names this machine in a menu or a notice: its host name
// with "(this machine)" after it, so a client attached to a session on
// another machine cannot take it for that one (#1281).
func thisMachineLabel() string {
	if n := localHostName(); n != "" {
		return n + " (this machine)"
	}
	return "this machine"
}

// thisMachineShort is this machine's host name alone, for a place with no
// room for the hint, or "this machine" when the name cannot be read.
func thisMachineShort() string {
	if n := localHostName(); n != "" {
		return n
	}
	return "this machine"
}
