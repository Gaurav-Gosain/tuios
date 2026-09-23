//go:build js

package session

import (
	"errors"
	"os"
)

// The browser build never runs a daemon: tuios runs standalone in the page and
// its panes are in-memory guests. These exist so the package compiles for
// js/wasm; the standalone path does not call them.

var errNoDaemonInBrowser = errors.New("no daemon in the browser build")

func lockFileExclusive(_ *os.File) error { return errNoDaemonInBrowser }

func (d *Daemon) handleSignals() { <-d.ctx.Done() }

// GetDaemonPID reports no daemon.
func GetDaemonPID() int { return 0 }
