//go:build unix

package app

import (
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"
)

// dumpTickStatsOnSignal writes the tick counters each time the process gets
// SIGUSR1, when TUIOS_STATS_FILE is set. The idle e2e reads them at the start
// and the end of its window. Without the variable nothing is installed. The
// goroutine sleeps on the signal channel, so it adds no wakeup of its own.
func dumpTickStatsOnSignal(p *tea.Program) {
	if p == nil || os.Getenv("TUIOS_STATS_FILE") == "" {
		return
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGUSR1)
	go func() {
		for range sigs {
			p.Send(tickStatsDumpMsg{})
		}
	}()
}
