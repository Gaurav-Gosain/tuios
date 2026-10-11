//go:build windows

package main

import "os"

// paneViewSignals end pane-view after its reset runs.
var paneViewSignals = []os.Signal{os.Interrupt}

// signalNumber is the number a shell adds to 128 for an interrupt.
func signalNumber(os.Signal) int { return 2 }
