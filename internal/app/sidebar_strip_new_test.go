package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A strip with no way to make a session is not a state of the rail, it is a
// state you have to leave to do anything. Each add control sits on its own
// list's header, beside the letter naming that list, which is the same binding
// the expanded rail's section headers make with the same glyph.
//
// It used to stack above the expand toggle at the strip's bottom edge, which is
// the placement the expanded rail's "+ new" was moved out of: down there it sat
// directly under the agents group and read as a control for that list.

// stripControl is the recorded slot of one of the strip's controls.
func stripControl(m *OS, kind sidebarStripRowKind) sidebarStripRow {
	for _, r := range m.sidebarStripRows {
		if r.Kind == kind {
			return r
		}
	}
	return sidebarStripRow{}
}

// TestStripAddCostsTheSpineNothing: the control stands in a line the head was
// already spending on a pad, so folding the rail does not cost a session mark.
func TestStripAddCostsTheSpineNothing(t *testing.T) {
	for _, h := range []int{20, 14, 9, 7, 6, 5} {
		with, tree := noAgentStripOS(t, 120, h)
		withMarks := len(stripMarkRows(railPlain(t, with, tree)))

		without, wtree := noAgentStripOS(t, 120, h)
		with.Settings = config.Global
		without.DaemonClient = nil // no session to make, so no control
		withoutMarks := len(stripMarkRows(railPlain(t, without, wtree)))

		if withMarks != withoutMarks {
			t.Errorf("h=%d: the strip shows %d marks with the add and %d without", h, withMarks, withoutMarks)
		}
	}
}

// stripMarkRows is the drawn lines carrying a spine or group mark, which is what
// a control competing for rows would take away.
func stripMarkRows(lines []string) []string {
	var out []string
	for _, ln := range lines {
		if strings.ContainsAny(ln, "·×▲■●⋮.") {
			out = append(out, ln)
		}
	}
	return out
}

// TestStripNewSessionIsClickableAcrossItsWholeRow: same rule as everything else
// on the strip, because a one-cell control on a three-column rail is the thing
// this round exists to stop drawing.
func TestStripNewSessionIsClickableAcrossItsWholeRow(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		for col := range 3 {
			m, tree := noAgentStripOS(t, 120, 20)
			withSidebar(t, true, pos, config.SidebarDefaultWidth)
			m.Settings = config.Global
			m.SidebarCollapsed = true
			m.sidebarPanelLinesForTree(tree)

			row := stripControl(m, sidebarStripHeader)
			railX0 := 0
			if pos == "right" {
				railX0 = m.GetRenderWidth() - m.GetSidebarWidth()
			}
			x := railX0 + col
			hit, ok := m.sidebarRowAt(x, row.Y0)
			if !ok || hit.Kind != sidebarRowNewSession {
				t.Fatalf("%s: column %d of the control hits %+v", pos, col, hit)
			}
			if hit.X0 != railX0 || hit.X1 != railX0+m.GetSidebarWidth() {
				t.Errorf("%s: the control claims %d..%d, want the whole band", pos, hit.X0, hit.X1)
			}
			// The edge rule is one of those columns, so the press has to hold the
			// click for the release rather than only arming the width handle.
			// Creating the session itself is a daemon round trip and is not driven
			// here; the routing is what the rectangle is for.
			if m.sidebarOnEdge(x) {
				m.SidebarClick(x, row.Y0, false)
				if !m.SidebarEdge.HaveRow || m.SidebarEdge.Row.Kind != sidebarRowNewSession {
					t.Errorf("%s: a press on the control's edge column holds %+v", pos, m.SidebarEdge.Row)
				}
			}
		}
	}
}

// TestStripNewSessionKeepsHitsAndNavIndexForIndex: the control is a nav row too,
// so the keyboard reaches it at the same index the pointer does.
func TestStripNewSessionKeepsHitsAndNavIndexForIndex(t *testing.T) {
	m, tree := agentStripOS(t, 120, 24)
	m.sidebarPanelLinesForTree(tree)

	if len(m.SidebarHits) != len(m.SidebarNav) {
		t.Fatalf("%d hits against %d nav rows", len(m.SidebarHits), len(m.SidebarNav))
	}
	idx := -1
	for i, n := range m.SidebarNav {
		if n.Kind == sidebarRowNewSession {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("the strip published no new-session row for the keyboard")
	}
	if m.SidebarHits[idx].Kind != sidebarRowNewSession {
		t.Errorf("hit %d is %v, want the control", idx, m.SidebarHits[idx].Kind)
	}
	// And it is where the expanded rail puts it: the slot before the first
	// session row, so the cursor walks the folded rail in the order it walks the
	// open one.
	first := -1
	for i, n := range m.SidebarNav {
		if n.Kind == sidebarRowSession {
			first = i
			break
		}
	}
	if first != idx+1 {
		t.Errorf("the control is nav row %d and the first session is %d; want it immediately above", idx, first)
	}
}

// TestStripNewSessionYieldsFirstOnAShortRail: it is the one thing on the strip
// that can wait for the rail to be reopened, so it goes before the badge and
// well before a session mark.
func TestStripNewSessionYieldsFirstOnAShortRail(t *testing.T) {
	for _, h := range []int{20, 9, 7, 6} {
		m, tree := stripOS(t, 120, h)
		lines := railPlain(t, m, tree)
		joined := strings.Join(lines, "\n")
		hasNew := strings.Contains(joined, "+")
		hasToggle := strings.Contains(joined, "»")
		// A rail down to four rows says what it has as the tail mark alone, which
		// is still the spine speaking.
		spine := strings.ContainsAny(joined, "·×▲■⋮")

		if !spine {
			t.Errorf("h=%d drew no session mark at all:\n%s", h, joined)
		}
		if hasNew && !hasToggle {
			t.Errorf("h=%d kept the new-session control over the way out:\n%s", h, joined)
		}
	}

	// With no daemon there is no session to make, so that control is absent
	// rather than drawn dead. Panes are still local, so the terminals list keeps
	// its own.
	m, tree := noAgentStripOS(t, 120, 20)
	m.Settings = config.Global
	m.DaemonClient = nil
	m.sidebarPanelLinesForTree(tree)
	if _, ok := sidebarHitOfKind(m, sidebarRowNewSession); ok {
		t.Error("a client that cannot create sessions still drew the control")
	}
	if _, ok := sidebarHitOfKind(m, sidebarRowNewWindow); !ok {
		t.Error("the terminals list lost its control along with the daemon")
	}
}
