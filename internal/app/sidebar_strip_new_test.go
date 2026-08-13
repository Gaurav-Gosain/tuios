package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// A strip with no way to make a session is not a state of the rail, it is a
// state you have to leave to do anything. The control lives at the bottom with
// the other one, on its own line, in the column every other mark is in.

// stripControl is the recorded slot of one of the strip's controls.
func stripControl(m *OS, kind sidebarStripRowKind) sidebarStripRow {
	for _, r := range m.sidebarStripRows {
		if r.Kind == kind {
			return r
		}
	}
	return sidebarStripRow{}
}

// TestStripNewSessionSitsAboveTheToggle: the two controls stack at the bottom,
// the toggle stays on the rail's last line but one where it has always been, and
// both sit in the pane-facing column with the spine's marks.
func TestStripNewSessionSitsAboveTheToggle(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		m, tree := noAgentStripOS(t, 120, 20)
		withSidebar(t, true, pos, config.SidebarDefaultWidth)
		m.SidebarCollapsed = true
		lines := railPlain(t, m, tree)
		rule := config.GetWindowBorderLeft()

		want := []string{" +", " »", "  "}
		if pos == "right" {
			// Mirrored: the pane-facing column is the other one, and the arrow
			// points the other way, exactly as the expanded rail's does.
			want = []string{"+ ", "« ", "  "}
		}
		for i, w := range want {
			line := w + rule
			if pos == "right" {
				line = rule + w
			}
			if got := lines[len(lines)-len(want)+i]; got != line {
				t.Errorf("%s: tail line %d = %q, want %q\n%s", pos, i, got, line, strings.Join(lines, "\n"))
			}
		}

		newRow, toggle := stripControl(m, sidebarStripNew), stripControl(m, sidebarStripToggle)
		if newRow.Y1 == 0 || toggle.Y1 == 0 {
			t.Fatalf("%s: the strip drew %v controls", pos, m.sidebarStripRows)
		}
		if newRow.Y0 >= toggle.Y0 {
			t.Errorf("%s: the new-session control is at %d, want it above the toggle at %d", pos, newRow.Y0, toggle.Y0)
		}
		if newRow.Label != "new session" {
			t.Errorf("%s: the control's label is %q", pos, newRow.Label)
		}
	}
}

// TestStripNewSessionIsClickableAcrossItsWholeRow: same rule as everything else
// on the strip, because a one-cell control on a three-column rail is the thing
// this round exists to stop drawing.
func TestStripNewSessionIsClickableAcrossItsWholeRow(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		for col := range 3 {
			m, tree := noAgentStripOS(t, 120, 20)
			withSidebar(t, true, pos, config.SidebarDefaultWidth)
			m.SidebarCollapsed = true
			m.sidebarPanelLinesForTree(tree)

			row := stripControl(m, sidebarStripNew)
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
	if idx != len(m.SidebarNav)-2 {
		t.Errorf("the control is nav row %d of %d, want it above the toggle at the end", idx, len(m.SidebarNav))
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

	// With no daemon there is no session to make, so the control is absent
	// rather than drawn dead.
	m, tree := noAgentStripOS(t, 120, 20)
	m.DaemonClient = nil
	if strings.Contains(strings.Join(railPlain(t, m, tree), ""), "+") {
		t.Error("a client that cannot create sessions still drew the control")
	}
}

// TestStripNewSessionASCIIAndMonochrome: the glyph is ASCII already and carries
// no colour of its own, so both modes degrade to the same cell.
func TestStripNewSessionASCIIAndMonochrome(t *testing.T) {
	prev := config.UseASCIIOnly
	config.UseASCIIOnly = true
	overlay.SetASCII(true)
	t.Cleanup(func() {
		config.UseASCIIOnly = prev
		overlay.SetASCII(prev)
	})

	m, tree := noAgentStripOS(t, 120, 20)
	lines := railPlain(t, m, tree)
	// The ASCII toggle is two cells wide, which is the other reason the two
	// controls never share a line.
	if got := lines[len(lines)-2]; got != ">>"+config.GetWindowBorderLeft() {
		t.Errorf("the ASCII toggle line is %q", got)
	}
	if got := lines[len(lines)-3]; got != " +"+config.GetWindowBorderLeft() {
		t.Errorf("the ASCII control line is %q", got)
	}
}
