package app

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// dockSessionOS is an OS with one live pane, which is the plainest dock the
// session controls can ride on.
func dockSessionOS(t testing.TB, width int, daemon bool) *OS {
	t.Helper()
	a := newTestWindow(t, "alpha", 60, 20)
	m := newTestOS(a)
	m.Windows = []*terminal.Window{a}
	a.Workspace = 1
	m.Width, m.Height = width, 40
	m.CurrentWorkspace = 1
	m.FocusedWindow = 0
	if daemon {
		m.IsDaemonSession = true
		m.DaemonClient = &session.TUIClient{}
		m.SessionName = "session-1"
	}
	return m
}

// dockSessionBody is the cells one control is drawn as: a pad, the glyph, a pad.
// The words are the hover label now and are never on the bar.
func dockSessionBody(a DockSessionAction) string {
	return " " + dockSessionIcon(a) + " "
}

// dockSessionColumns finds where a control was actually drawn on the dock row,
// which is the geometry a click has to agree with. It reads the rendered frame
// rather than the layout arithmetic, because agreeing with the arithmetic is
// exactly what a broken hit rect already does.
func dockSessionColumns(t *testing.T, m *OS, body string) (x0, x1 int) {
	t.Helper()
	dock, top := m.renderDockString()
	lines := strings.Split(dock, "\n")
	r := m.GetDockbarContentYPosition() - top
	if r < 0 || r >= len(lines) {
		t.Fatalf("the dock's content row %d is outside the %d rows it drew", r, len(lines))
	}
	row := stripANSIForTrace(lines[r])
	i := strings.LastIndex(row, body)
	if i < 0 {
		t.Fatalf("the dock never drew the control %q:\n%q", body, row)
	}
	// Columns, not bytes: the control's glyph is multi-byte, so a byte offset
	// lands left of the button and hides a real misalignment.
	x0 = lipgloss.Width(row[:i])
	return x0, x0 + lipgloss.Width(body)
}

// TestDockSessionControlsAreClickableWhereTheyAreDrawn is the invariant the
// minimized entries had to learn the hard way: the hit rect is what the
// renderer drew, at every width, including the button's first and last column.
//
// The controls are three cells now, so the edge columns are most of them: a rect
// off by one is a third of the target gone rather than a fifth.
func TestDockSessionControlsAreClickableWhereTheyAreDrawn(t *testing.T) {
	for _, width := range []int{160, 100, 40} {
		for _, pos := range []string{"bottom", "top"} {
			t.Run(strconv.Itoa(width)+"/"+pos, func(t *testing.T) {
				prev := config.DockbarPosition
				config.DockbarPosition = pos
				t.Cleanup(func() { config.DockbarPosition = prev })

				m := dockSessionOS(t, width, true)
				if !dockSessionControlsFit(width) {
					t.Fatalf("width %d drew no controls at all", width)
				}
				y := m.GetDockbarContentYPosition()

				for _, want := range []DockSessionAction{DockSessionLeave, DockSessionClose} {
					body := dockSessionBody(want)
					x0, x1 := dockSessionColumns(t, m, body)

					// Both edges, and everything between them.
					if got := m.DockSessionActionAt(x0, y); got != want {
						t.Errorf("the control's first column %d routed to %v, want %v", x0, got, want)
					}
					if got := m.DockSessionActionAt(x1-1, y); got != want {
						t.Errorf("the control's last column %d routed to %v, want %v", x1-1, got, want)
					}
					for x := x0; x < x1; x++ {
						if got := m.DockSessionActionAt(x, y); got != want {
							t.Errorf("column %d routed to %v, want %v", x, got, want)
						}
					}
					// One column past either edge is not this control.
					if got := m.DockSessionActionAt(x0-1, y); got == want {
						t.Errorf("the column left of the control still routed to %v", want)
					}
					if got := m.DockSessionActionAt(x1, y); got == want {
						t.Errorf("the column right of the control still routed to %v", want)
					}
					// The dock is one row.
					above := y - 1
					if pos == "top" {
						above = y + 1
					}
					if got := m.DockSessionActionAt(x0, above); got != DockSessionNone {
						t.Errorf("the row off the dock routed to %v", got)
					}
				}

				// The destructive control does not sit on the screen's last
				// column: a pointer thrown at the right edge has to stop on a
				// cell that does nothing.
				_, closeX1 := dockSessionColumns(t, m, dockSessionBody(DockSessionClose))
				if closeX1 != width-1 {
					t.Errorf("the close control ends at column %d, want %d so a bare column is left at the edge", closeX1, width-1)
				}
				if got := m.DockSessionActionAt(width-1, y); got != DockSessionNone {
					t.Errorf("the screen's last column routed to %v, want nothing there", got)
				}
			})
		}
	}
}

// TestDockSessionControlsSurviveStaleState checks the hit rects answer for the
// frame on screen. The controls are placed against the bar's total width, which
// the mode pill and the meters both move on their own.
func TestDockSessionControlsSurviveStaleState(t *testing.T) {
	m := dockSessionOS(t, 160, true)
	body := dockSessionBody(DockSessionClose)
	x0, x1 := dockSessionColumns(t, m, body)
	y := m.GetDockbarContentYPosition()

	m.Mode = TerminalMode
	for x := x0; x < x1; x++ {
		if got := m.DockSessionActionAt(x, y); got != DockSessionClose {
			t.Fatalf("column %d routed to %v after unrelated state moved on; the hit test is recomputing, not reading the frame", x, got)
		}
	}
}

// TestLeaveRunningNeedsADaemon is the correctness requirement: under plain
// tuios the panes belong to this process, so there is nothing to leave running
// and the control must be absent rather than dead.
func TestLeaveRunningNeedsADaemon(t *testing.T) {
	for _, tc := range []struct {
		name   string
		daemon bool
		want   bool
	}{
		{"no daemon", false, false},
		{"daemon", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := dockSessionOS(t, 160, tc.daemon)
			if got := m.CanLeaveRunning(); got != tc.want {
				t.Fatalf("CanLeaveRunning() = %v, want %v", got, tc.want)
			}

			dock, _ := m.renderDockString()
			row := stripANSIForTrace(dock)
			drawn := strings.Contains(row, dockSessionBody(DockSessionLeave))
			if drawn != tc.want {
				t.Errorf("the dock drew the leave control = %v, want %v:\n%q", drawn, tc.want, row)
			}

			// Absent means no hit rect either: a control nobody can see must
			// not be clickable where it would have been.
			for _, h := range m.dockSessionHits {
				if h.Action == DockSessionLeave && !tc.want {
					t.Errorf("the leave control kept a hit rect %v with no daemon", h)
				}
			}

			// Closing is offered on every run path, since every run path can
			// end the thing it is running.
			if !strings.Contains(row, dockSessionBody(DockSessionClose)) {
				t.Errorf("the dock never drew the close control:\n%q", row)
			}
		})
	}
}

// TestDockSessionStripWidthMatchesWhatIsDrawn keeps the layout pass and the
// render pass on the same number. They are two calls, and the bar is laid out
// against one of them and drawn against the other.
func TestDockSessionStripWidthMatchesWhatIsDrawn(t *testing.T) {
	for _, width := range []int{160, 100, 40, 20} {
		for _, daemon := range []bool{true, false} {
			m := dockSessionOS(t, width, daemon)
			strip, _ := m.buildDockSessionStrip()
			if got, want := m.dockSessionStripWidth(), lipgloss.Width(strip); got != want {
				t.Errorf("width %d daemon %v: layout reserved %d columns, the renderer drew %d", width, daemon, got, want)
			}
		}
	}
}

// TestDockSessionControlsAreDroppedOnATinyDock checks the strip gives way when
// the bar has nothing left, rather than pushing the dock off the screen.
func TestDockSessionControlsAreDroppedOnATinyDock(t *testing.T) {
	m := dockSessionOS(t, dockSessionIconMinWidth-1, true)
	if strip, _ := m.buildDockSessionStrip(); strip != "" {
		t.Fatalf("a %d column dock still drew the controls: %q", dockSessionIconMinWidth-1, strip)
	}
	dock, _ := m.renderDockString()
	for _, line := range strings.Split(dock, "\n") {
		if w := lipgloss.Width(line); w > dockSessionIconMinWidth-1 {
			t.Errorf("the dock drew %d columns on a %d column screen", w, dockSessionIconMinWidth-1)
		}
	}
}

// TestDockSessionHoverTracksThePointer checks the recessed control brightens
// only where a click would land, and goes back to muted on the way out.
func TestDockSessionHoverTracksThePointer(t *testing.T) {
	m := dockSessionOS(t, 160, true)
	x0, _ := dockSessionColumns(t, m, dockSessionBody(DockSessionClose))
	y := m.GetDockbarContentYPosition()

	if !m.DockSessionHoverAt(x0, y) || m.dockSessionHover != DockSessionClose {
		t.Fatalf("hovering the close control set %v", m.dockSessionHover)
	}
	if m.DockSessionHoverAt(0, y) || m.dockSessionHover != DockSessionNone {
		t.Fatalf("moving off the controls left the highlight on %v", m.dockSessionHover)
	}
}
