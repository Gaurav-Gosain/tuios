//go:build !windows

package main

import (
	"os"
	"syscall"
)

// paneViewSignals end pane-view after its reset runs.
var paneViewSignals = []os.Signal{syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT}

// signalNumber is the number a shell adds to 128 for sig.
func signalNumber(sig os.Signal) int {
	if s, ok := sig.(syscall.Signal); ok {
		return int(s)
	}
	return 1
}
