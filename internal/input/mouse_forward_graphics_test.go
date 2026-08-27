package input

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// mouseModeGraphicsWindow builds a focused, full-content window whose emulator
// has enabled the same mouse modes terminal-browser uses (any-motion + SGR +
// SGR-pixel), sized so cell (5,3) maps to a known host pixel.
func mouseModeGraphicsWindow(t *testing.T) (*app.OS, *vt.Emulator) {
	t.Helper()
	em := vt.NewEmulator(80, 24)
	t.Cleanup(func() { _ = em.Close() })
	_, _ = em.Write([]byte("\x1b[?1003h\x1b[?1006h\x1b[?1016h"))
	em.SetCellSize(10, 20)

	win := &terminal.Window{Terminal: em, X: 0, Y: 0, Width: 82, Height: 26}
	o := &app.OS{
		Settings:      config.Global,
		Mode:          app.TerminalMode,
		FocusedWindow: 0,
		Windows:       []*terminal.Window{win},
	}
	return o, em
}

// TestWheelForwardedToMouseModeGraphicsPane proves a wheel event over a
// mouse-tracking pane reaches that pane's emulator instead of being swallowed by
// tuios scrollback / copy mode (the natural-scroll routing is only for panes
// WITHOUT mouse mode). With 1016 on it must carry pixel coordinates.
func TestWheelForwardedToMouseModeGraphicsPane(t *testing.T) {
	// Pin one report per notch so this asserts the coordinate encoding, not the
	// amplification (covered by TestWheelAmplifiedByScrollLines).
	defer withScrollLines(1)()

	o, em := mouseModeGraphicsWindow(t)

	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 128)
		n, _ := em.Read(buf)
		got <- string(buf[:n])
	}()

	// Wheel-up at screen cell (5,3); content offset is 1 (border), so pane cell
	// is (4,2) -> pixel centre (4*10+5, 2*20+10) = (45, 50) -> SGR 1-based 46;51.
	handleMouseWheel(tea.MouseWheelMsg(tea.Mouse{X: 5, Y: 3, Button: tea.MouseWheelUp}), o)

	select {
	case s := <-got:
		if s != "\x1b[<64;46;51M" {
			t.Fatalf("wheel report = %q, want %q (pixel coords for mouse-mode graphics pane)", s, "\x1b[<64;46;51M")
		}
	case <-time.After(time.Second):
		t.Fatal("wheel was not forwarded to the mouse-mode pane (consumed by scrollback/copy mode?)")
	}

	// The wheel must not have entered copy mode on a mouse-mode pane.
	if o.Windows[0].InCopyMode() {
		t.Fatal("wheel over a mouse-mode pane wrongly entered copy mode")
	}
}

// withScrollLines sets config.ScrollLines and returns a restore func.
func withScrollLines(n int) func() {
	prev := config.Global.ScrollLines
	config.Global.ScrollLines = n
	return func() { config.Global.ScrollLines = prev }
}

// TestWheelAmplifiedByScrollLines proves one physical notch over a mouse-mode
// pane forwards config.ScrollLines wheel reports, so browsers and other
// mouse-reporting apps scroll a comfortable distance per notch instead of one
// tiny step.
func TestWheelAmplifiedByScrollLines(t *testing.T) {
	defer withScrollLines(3)()

	o, em := mouseModeGraphicsWindow(t)

	const want = "\x1b[<64;46;51M"
	got := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 256)
		// Read until the three reports have arrived (or the reader is drained).
		for b.Len() < len(want)*3 {
			n, err := em.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		got <- b.String()
	}()

	handleMouseWheel(tea.MouseWheelMsg(tea.Mouse{X: 5, Y: 3, Button: tea.MouseWheelUp}), o)

	select {
	case s := <-got:
		if s != strings.Repeat(want, 3) {
			t.Fatalf("wheel reports = %q, want three copies of %q", s, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wheel was not amplified to config.ScrollLines reports")
	}
}

// TestWheelForwardedAfterDaemonModeRestore is the routing half of the daemon
// reattach fix: the same wheel forwarding must hold when the emulator learned
// its mouse modes from the daemon's snapshot (RestoreModes, as every attach
// and session switch applies it) rather than by parsing the guest's DECSET
// sequences, which have scrolled out of the daemon's bounded output buffer by
// the time a client reattaches.
func TestWheelForwardedAfterDaemonModeRestore(t *testing.T) {
	// Pin one report per notch so this asserts routing after mode restore, not
	// the scroll-speed amplification (covered by TestWheelAmplifiedByScrollLines).
	defer withScrollLines(1)()

	em := vt.NewEmulator(80, 24)
	t.Cleanup(func() { _ = em.Close() })
	em.RestoreModes(map[int]bool{1003: true, 1006: true, 1016: true})
	em.SetCellSize(10, 20)

	win := &terminal.Window{Terminal: em, X: 0, Y: 0, Width: 82, Height: 26}
	o := &app.OS{
		Settings:      config.Global,
		Mode:          app.TerminalMode,
		FocusedWindow: 0,
		Windows:       []*terminal.Window{win},
	}

	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 128)
		n, _ := em.Read(buf)
		got <- string(buf[:n])
	}()

	handleMouseWheel(tea.MouseWheelMsg(tea.Mouse{X: 5, Y: 3, Button: tea.MouseWheelUp}), o)

	select {
	case s := <-got:
		if s != "\x1b[<64;46;51M" {
			t.Fatalf("wheel report = %q, want %q (pixel coords for restored mouse-mode pane)", s, "\x1b[<64;46;51M")
		}
	case <-time.After(time.Second):
		t.Fatal("wheel was not forwarded to the pane after mode restore (HasMouseMode cache stale?)")
	}
	if o.Windows[0].InCopyMode() {
		t.Fatal("wheel over a restored mouse-mode pane wrongly entered copy mode")
	}
}

// TestMotionForwardedToMouseModeGraphicsPane proves hover (motion) reaches a
// mouse-mode pane with pixel coordinates when 1016 is on.
func TestMotionForwardedToMouseModeGraphicsPane(t *testing.T) {
	o, em := mouseModeGraphicsWindow(t)

	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 128)
		n, _ := em.Read(buf)
		got <- string(buf[:n])
	}()

	// Motion at screen cell (5,3) -> pane cell (4,2) -> pixel 46;51, button 35
	// is the SGR "motion, no button" code (32 + 3 for the motion bit path).
	handleMouseMotion(tea.MouseMotionMsg(tea.Mouse{X: 5, Y: 3, Button: tea.MouseNone}), o)

	select {
	case s := <-got:
		if s != "\x1b[<35;46;51M" {
			t.Fatalf("motion report = %q, want %q (pixel hover for mouse-mode graphics pane)", s, "\x1b[<35;46;51M")
		}
	case <-time.After(time.Second):
		t.Fatal("motion (hover) was not forwarded to the mouse-mode pane")
	}
}
