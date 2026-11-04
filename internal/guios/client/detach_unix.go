//go:build !windows

package client

import "os"

// Interrupt asks the daemon of a pid to save and stop.
func Interrupt(p *os.Process) error { return p.Signal(os.Interrupt) }
