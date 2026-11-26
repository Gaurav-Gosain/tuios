//go:build !unix || js

package session

import "time"

const paneReapGrace = time.Second

type reapProc struct{}

func (d *Daemon) paneProcs() []reapProc { return nil }

func reapPaneProcs([]reapProc, time.Duration) int { return 0 }
