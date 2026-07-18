package input

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// windowWithScrollback builds a window whose emulator has more history than
// fits on screen, without spawning a PTY.
func windowWithScrollback(t *testing.T) *terminal.Window {
	t.Helper()
	em := vt.NewEmulator(20, 5)
	t.Cleanup(func() { _ = em.Close() })
	for i := range 40 {
		_, _ = em.Write([]byte(fmt.Sprintf("line %d\r\n", i)))
	}
	if em.ScrollbackLen() == 0 {
		t.Fatal("emulator produced no scrollback; the test cannot exercise scrolling")
	}
	return &terminal.Window{Terminal: em, Width: 22, Height: 7}
}

// The wheel step used to be hardcoded to 3 lines everywhere, so users on
// high-resolution wheels or large monitors had no way to make scrollback move
// faster. It now follows appearance.scroll_lines.
func TestMouseWheelUsesConfiguredScrollLines(t *testing.T) {
	prev := config.ScrollLines
	t.Cleanup(func() { config.ScrollLines = prev })

	for _, step := range []int{1, 3, 10} {
		t.Run(fmt.Sprintf("step %d", step), func(t *testing.T) {
			config.ScrollLines = step
			win := windowWithScrollback(t)
			o := &app.OS{
				Mode:          app.TerminalMode,
				SelectionMode: true,
				FocusedWindow: 0,
				Windows:       []*terminal.Window{win},
			}

			handleMouseWheel(tea.MouseWheelMsg{Button: tea.MouseWheelUp}, o)
			if win.ScrollbackOffset != step {
				t.Fatalf("after one wheel-up ScrollbackOffset = %d, want %d", win.ScrollbackOffset, step)
			}

			handleMouseWheel(tea.MouseWheelMsg{Button: tea.MouseWheelDown}, o)
			if win.ScrollbackOffset != 0 {
				t.Errorf("after wheel-down ScrollbackOffset = %d, want 0", win.ScrollbackOffset)
			}
		})
	}
}

// Scrolling must still stop at the ends of the buffer whatever the step is.
func TestMouseWheelClampsAtScrollbackBounds(t *testing.T) {
	prev := config.ScrollLines
	t.Cleanup(func() { config.ScrollLines = prev })
	config.ScrollLines = 50

	win := windowWithScrollback(t)
	o := &app.OS{
		Mode:          app.TerminalMode,
		SelectionMode: true,
		FocusedWindow: 0,
		Windows:       []*terminal.Window{win},
	}

	limit := win.ScrollbackLen()
	for range 5 {
		handleMouseWheel(tea.MouseWheelMsg{Button: tea.MouseWheelUp}, o)
	}
	if win.ScrollbackOffset != limit {
		t.Errorf("ScrollbackOffset = %d, want it clamped to scrollback length %d", win.ScrollbackOffset, limit)
	}

	for range 5 {
		handleMouseWheel(tea.MouseWheelMsg{Button: tea.MouseWheelDown}, o)
	}
	if win.ScrollbackOffset != 0 {
		t.Errorf("ScrollbackOffset = %d, want 0", win.ScrollbackOffset)
	}
}
