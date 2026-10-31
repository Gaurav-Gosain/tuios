package app

import (
	"sync"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// TestSetThemeWhileAPanePrints is a race regression. A pane's writer reads the
// palette while it parses a colour, under the window's IO lock. SetTheme writes
// the palette, so it has to hold that lock too. Without the lock, -race reports
// the pair within 20 ms. TestEventContract in internal/learn hit the same pair,
// but only on the nights a prompt happened to land on the theme change.
func TestSetThemeWhileAPanePrints(t *testing.T) {
	t.Cleanup(func() { _ = theme.Initialize("") })
	w := newTestWindow(t, "theme-race", 40, 10)
	m := &OS{Windows: []*terminal.Window{w}}

	stop := make(chan struct{})
	var printing sync.WaitGroup
	printing.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				w.WriteOutputAsync([]byte("\x1b[31mred\x1b[m\r\n"))
			}
		}
	})
	for i := range 100 {
		name := "dracula"
		if i%2 == 1 {
			name = "nord"
		}
		if err := m.SetTheme(name); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	printing.Wait()
}
