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

// TestMouseForwardedToMouseModeGraphicsPane pins the reports a mouse-tracking
// pane receives with the modes terminal-browser uses (any-motion + SGR +
// SGR-pixel). A wheel over it must reach its emulator instead of tuios
// scrollback or copy mode, amplified to config.ScrollLines reports per notch,
// and with 1016 on every report carries pixel coordinates. restored teaches the
// emulator its modes from a daemon snapshot (RestoreModes, as every attach and
// session switch applies it) rather than by parsing DECSET, since those
// sequences have scrolled out of the daemon's bounded buffer by the time a
// client reattaches.
func TestMouseForwardedToMouseModeGraphicsPane(t *testing.T) {
	// Screen cell (5,3); content offset is 1 (border), so pane cell is (4,2) ->
	// pixel centre (4*10+5, 2*20+10) = (45, 50) -> SGR 1-based 46;51.
	wheel := tea.MouseWheelMsg(tea.Mouse{X: 5, Y: 3, Button: tea.MouseWheelUp})
	const wheelReport = "\x1b[<64;46;51M"

	for _, tc := range []struct {
		name     string
		restored bool
		lines    int
		msg      tea.Msg
		want     string
	}{
		{"wheel", false, 1, wheel, wheelReport},
		{"wheel amplified by scroll lines", false, 3, wheel, strings.Repeat(wheelReport, 3)},
		{"wheel after daemon mode restore", true, 1, wheel, wheelReport},
		// Button 35 is the SGR "motion, no button" code.
		{"motion", false, 1, tea.MouseMotionMsg(tea.Mouse{X: 5, Y: 3, Button: tea.MouseNone}), "\x1b[<35;46;51M"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prev := config.Global.ScrollLines
			config.Global.ScrollLines = tc.lines
			t.Cleanup(func() { config.Global.ScrollLines = prev })

			em := vt.NewEmulator(80, 24)
			t.Cleanup(func() { _ = em.Close() })
			if tc.restored {
				em.RestoreModes(map[int]bool{1003: true, 1006: true, 1016: true})
			} else {
				_, _ = em.Write([]byte("\x1b[?1003h\x1b[?1006h\x1b[?1016h"))
			}
			em.SetCellSize(10, 20)
			win := &terminal.Window{Terminal: em, X: 0, Y: 0, Width: 82, Height: 26}
			o := &app.OS{Settings: config.Global, Mode: app.TerminalMode, FocusedWindow: 0, Windows: []*terminal.Window{win}}

			got := make(chan string, 1)
			go func() {
				var b strings.Builder
				buf := make([]byte, 256)
				for b.Len() < len(tc.want) {
					n, err := em.Read(buf)
					b.Write(buf[:n])
					if err != nil {
						break
					}
				}
				got <- b.String()
			}()

			switch msg := tc.msg.(type) {
			case tea.MouseWheelMsg:
				handleMouseWheel(msg, o)
			case tea.MouseMotionMsg:
				handleMouseMotion(msg, o)
			}

			select {
			case s := <-got:
				if s != tc.want {
					t.Fatalf("report = %q, want %q", s, tc.want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("the event was not forwarded to the mouse-mode pane")
			}
			if win.InCopyMode() {
				t.Fatal("the event over a mouse-mode pane entered copy mode")
			}
		})
	}
}

// TestGuestMotionForwardingFollowsGuestMouseMode guards what the host's
// all-motion tracking must not leak: a guest sees only the motion its own mode
// would have reported.
func TestGuestMotionForwardingFollowsGuestMouseMode(t *testing.T) {
	cases := []struct {
		name   string
		enable string
		button tea.MouseButton
		want   bool
	}{
		{"any-event, button free", "\x1b[?1003h", tea.MouseNone, true},
		{"any-event, button held", "\x1b[?1003h", tea.MouseLeft, true},
		{"button-event, button free", "\x1b[?1002h", tea.MouseNone, false},
		{"button-event, button held", "\x1b[?1002h", tea.MouseLeft, true},
		{"normal tracking, button free", "\x1b[?1000h", tea.MouseNone, false},
		{"normal tracking, button held", "\x1b[?1000h", tea.MouseLeft, false},
		{"no mouse mode", "", tea.MouseLeft, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			em := vt.NewEmulator(38, 18)
			t.Cleanup(func() { _ = em.Close() })
			if tc.enable != "" {
				if _, err := em.Write([]byte(tc.enable)); err != nil {
					t.Fatalf("enable mouse tracking: %v", err)
				}
			}
			if got := guestWantsMotion(em, tc.button); got != tc.want {
				t.Errorf("forward = %v, want %v", got, tc.want)
			}
		})
	}
}
