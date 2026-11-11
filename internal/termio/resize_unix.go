//go:build !windows && !js

package termio

import (
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
)

// WatchResize sends the console's size to send each time the terminal
// resizes it, which Bubble Tea does itself only when it runs a renderer. It
// returns what stops the watch.
func (s *Screen) WatchResize(send func(tea.Msg)) (stop func()) {
	if s.console == nil {
		return func() {}
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ch:
				if w, h, err := term.GetSize(s.console.Fd()); err == nil {
					send(tea.WindowSizeMsg{Width: w, Height: h})
				}
			}
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}
