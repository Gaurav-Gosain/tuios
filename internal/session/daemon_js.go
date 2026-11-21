//go:build js

package session

import (
	"errors"
	"os"
)

var errNoDaemonInBrowser = errors.New("no daemon in the browser build")

func lockFileExclusive(_ *os.File) error { return errNoDaemonInBrowser }

func (d *Daemon) handleSignals() { <-d.ctx.Done() }

// GetDaemonPID reports no daemon.
func GetDaemonPID() int { return 0 }
