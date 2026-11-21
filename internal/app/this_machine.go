package app

import (
	"os"
	"runtime"
	"strings"
	"sync"
)

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

func thisMachineLabel() string {
	if n := localHostName(); n != "" {
		return n + " (this machine)"
	}
	return "this machine"
}

func thisMachineShort() string {
	if n := localHostName(); n != "" {
		return n
	}
	return "this machine"
}
