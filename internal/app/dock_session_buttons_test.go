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
	return " " + dockSessionIcon(a, &config.Global) + " "
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
				prev := config.Global.DockbarPosition
				config.Global.DockbarPosition = pos
				t.Cleanup(func() { config.Global.DockbarPosition = prev })

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
