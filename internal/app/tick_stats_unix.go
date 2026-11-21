//go:build unix

package app

import (
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"
)

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
