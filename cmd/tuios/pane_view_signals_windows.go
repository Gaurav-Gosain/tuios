//go:build windows

package main

import "os"

var paneViewSignals = []os.Signal{os.Interrupt}

func signalNumber(os.Signal) int { return 2 }
