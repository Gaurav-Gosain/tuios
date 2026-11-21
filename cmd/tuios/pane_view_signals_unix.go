//go:build !windows

package main

import (
	"os"
	"syscall"
)

var paneViewSignals = []os.Signal{syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT}

func signalNumber(sig os.Signal) int {
	if s, ok := sig.(syscall.Signal); ok {
		return int(s)
	}
	return 1
}
