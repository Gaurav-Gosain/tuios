package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/charmbracelet/x/ansi"
)

// The rail's one add affordance used to be "+ new" pinned to the bottom edge,
// directly under the agents block, where it read as "new agent". These pin the
// replacement: a "+" in the header of each section that can make another of
// what it lists, and nothing in the agents header, because an agent is a pane
// running an agent CLI and the terminals "+" already makes one of those.

// addHits returns the recorded add-control rects, in drawn order.
func addHits(m *OS) []sidebarRowHit {
	var out []sidebarRowHit
	for _, h := range m.SidebarHits {
		if sidebarAddKind(h.Kind) {
			out = append(out, h)
		}
	}
	return out
}

// TestAddControlRectsMatchTheirDrawnCells is the rule for every affordance the
// renderer records: the rectangle claims exactly the cells the glyph occupies,
// at both its edge columns, at every width and on both sides.
func TestAddControlRectsMatchTheirDrawnCells(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		for _, width := range []int{config.SidebarDefaultWidth, 20, config.SidebarNarrowWidth} {
			t.Run(fmt.Sprintf("%s/w=%d", pos, width), func(t *testing.T) {
				m := daemonRailOS(t, 120, 40)
				withSidebar(t, true, pos, width)
				lines, w := m.sidebarPanelLines()

				hits := addHits(m)
				if len(hits) != 2 {
					t.Fatalf("drew %d add controls, want the sessions and terminals headers", len(hits))
				}
				railX0 := 0
				if pos == "right" {
					railX0 = m.GetRenderWidth() - w
				}
				for _, h := range hits {
					row := []rune(ansi.Strip(lines[h.Y0-m.GetTopMargin()]))
					// Both edge columns: the first cell the rect claims carries the
					// glyph, and the cell past its end does not.
					first := h.X0 - railX0
					if first < 0 || first >= len(row) || string(row[first]) != sidebarAddGlyph {
						t.Errorf("%v: rect starts at column %d, which draws %q not %q",
							h.Kind, first, safeRune(row, first), sidebarAddGlyph)
					}
					if last := h.X1 - railX0; last < len(row) && string(row[last]) == sidebarAddGlyph {
						t.Errorf("%v: the cell past the rect at column %d still draws the glyph", h.Kind, last)
					}
					// And the rect is exactly the glyph's width.
					if h.X1-h.X0 != len([]rune(sidebarAddGlyph)) {
						t.Errorf("%v: rect is %d cells for a %d-cell glyph", h.Kind, h.X1-h.X0, len([]rune(sidebarAddGlyph)))
					}
				}
			})
		}
	}
}

// TestAgentsHeaderHasNoAddControl states the asymmetry out loud, so a later
// round adding one has to argue with this rather than with a silence.
func TestAgentsHeaderHasNoAddControl(t *testing.T) {
	m, tree := sidebarMultiSessionOS(t, 120, 40)
	m.DaemonClient = nil // the sessions "+" is hidden without one, the agents one never existed
	lines := railPlain(t, m, tree)

	agentsLine := lineOf(lines, " agents")
	if agentsLine < 0 {
		t.Fatal("the fixture drew no agents header")
	}
	if strings.Contains(lines[agentsLine], sidebarAddGlyph) {
		t.Errorf("the agents header reads %q, want no add control on it", lines[agentsLine])
	}
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowNewSession || h.Kind == sidebarRowNewWindow {
			if h.Y0-m.GetTopMargin() == agentsLine {
				t.Error("an add control was recorded on the agents header")
			}
		}
	}
}

// TestAddControlsAreReachableByKeyboard: a rect with no nav row is a control
// only the mouse can work, which the rail does not have.
func TestAddControlsAreReachableByKeyboard(t *testing.T) {
	m := daemonRailOS(t, 120, 40)
	m.SidebarFocused = true
	m.sidebarPanelLines()
	// The rows are published; the client behind them has no daemon on the other
	// end, so creating a session goes down its "say so" path rather than the wire.
	m.DaemonClient = nil

	for _, h := range addHits(m) {
		found := -1
		for i, n := range m.SidebarNav {
			if sidebarNavRowsEqual(n, navRowOf(h)) {
				found = i
			}
		}
		if found < 0 {
			t.Fatalf("%v has a rect but no nav row", h.Kind)
		}
		// And activating it from the cursor runs the same thing a click does.
		m.SidebarCursor = found
		before := len(m.Windows)
		exits := m.SidebarActivateCursor()
		switch h.Kind {
		case sidebarRowNewWindow:
			if len(m.Windows) != before+1 {
				t.Errorf("enter on the terminals + made %d panes, want one", len(m.Windows)-before)
			}
			if !exits {
				t.Error("enter on the terminals + did not hand the keyboard to the new pane")
			}
		case sidebarRowNewSession:
			if exits {
				t.Error("enter on the sessions + left the rail")
			}
		}
	}
}

// TestTheWalkStepsOntoTheAddControlAndTheSectionKeyStepsOverIt is the settled
// answer to whether a control that already has a key of its own belongs in the
// j/k sequence.
//
// It does. The walk is the rail's one route that depends on no binding but j
// and k, so a control left out of it is reachable only through the key it
// happens to be bound to: rebind that key away and the affordance is
// mouse-only. Its slot is not free to move either, since the hits are recorded
// as the renderer draws and the cursor walks them in that order, so a control
// drawn on a header sits above that section's rows or the two lists disagree.
//
// What that costs is one step crossing from the sessions into the terminals,
// and the rail already sells the way round it: the section keys land on the
// first row of the next list and step over every control on the way.
func TestTheWalkStepsOntoTheAddControlAndTheSectionKeyStepsOverIt(t *testing.T) {
	m, _ := railOS(t)

	lastSession := -1
	for i, r := range m.SidebarNav {
		if r.Kind == sidebarRowSession {
			lastSession = i
		}
	}
	if lastSession < 0 {
		t.Fatal("the rail published no session rows")
	}

	m.SidebarCursor = lastSession
	m.SidebarCursorMove(1)
	if row, ok := m.sidebarCursorRow(); !ok || row.Kind != sidebarRowNewWindow {
		t.Fatalf("j off the last session row landed on %+v, want the terminals +", row)
	}
	m.SidebarCursorMove(1)
	if row, ok := m.sidebarCursorRow(); !ok || row.Kind != sidebarRowWindow {
		t.Fatalf("j off the terminals + landed on %+v, want the first pane row", row)
	}

	m.SidebarCursor = lastSession
	m.SidebarCursorExpand()
	if row, ok := m.sidebarCursorRow(); !ok || row.Kind != sidebarRowWindow {
		t.Fatalf("the section key landed on %+v, want the first pane row", row)
	}
}

// TestAddControlsHaveTooltips: they are the only thing on the expanded rail
// drawn as a bare glyph, which is exactly the condition for a label.
func TestAddControlsHaveTooltips(t *testing.T) {
	prev := config.Tooltips
	config.Tooltips = true
	t.Cleanup(func() { config.Tooltips = prev })

	m := daemonRailOS(t, 120, 40)
	m.sidebarPanelLines()

	for _, h := range addHits(m) {
		m.tooltipClear()
		m.SidebarMotion(h.X0, h.Y0)
		if m.Tooltip.Source != tooltipRailAdd {
			t.Errorf("%v: hovering it tracked source %v, want the rail's add label", h.Kind, m.Tooltip.Source)
			continue
		}
		if sidebarRowKind(m.Tooltip.Key) != h.Kind {
			t.Errorf("%v: the label is keyed to %v", h.Kind, sidebarRowKind(m.Tooltip.Key))
		}
		// The words say what it makes, and the two controls do not say the same.
		if got := sidebarAddWords(h.Kind); got == "" {
			t.Errorf("%v has no words", h.Kind)
		}
	}
	if sidebarAddWords(sidebarRowNewSession) == sidebarAddWords(sidebarRowNewWindow) {
		t.Error("both add controls say the same thing")
	}
}

// TestAddControlsDegradeToASCII, since the rail supports it. "+" is ASCII
// already, so this is a guard against a later round reaching for a nicer glyph.
func TestAddControlsDegradeToASCII(t *testing.T) {
	prev := config.UseASCIIOnly
	config.UseASCIIOnly = true
	overlay.SetASCII(true)
	t.Cleanup(func() {
		config.UseASCIIOnly = prev
		overlay.SetASCII(prev)
	})

	m := daemonRailOS(t, 120, 40)
	lines, _ := m.sidebarPanelLines()
	joined := ansi.Strip(strings.Join(lines, "\n"))
	for _, r := range joined {
		if r > 0x7f {
			t.Fatalf("the ASCII rail drew %q:\n%s", r, joined)
		}
	}
	if len(addHits(m)) != 2 {
		t.Error("the ASCII rail lost its add controls")
	}
}

// TestNarrowHeaderDropsTheAddControlWholeOrNotAtAll: half a control is half a
// click target, so a header with no room draws none and records none.
func TestNarrowHeaderDropsTheAddControlWholeOrNotAtAll(t *testing.T) {
	for w := config.SidebarGlyphWidth + 1; w <= config.SidebarNarrowWidth; w++ {
		m := daemonRailOS(t, 120, 40)
		withSidebar(t, true, "left", w)
		lines, railW := m.sidebarPanelLines()
		if sidebarVariant(railW) == sidebarVariantGlyph {
			continue // the strip draws its own controls
		}
		for _, h := range addHits(m) {
			row := []rune(ansi.Strip(lines[h.Y0-m.GetTopMargin()]))
			if h.X0 < 0 || h.X1 > railW {
				t.Errorf("w=%d: %v spans [%d,%d) outside a %d-wide rail", w, h.Kind, h.X0, h.X1, railW)
			}
			if c := h.X0; c >= len(row) || string(row[c]) != sidebarAddGlyph {
				t.Errorf("w=%d: %v claims column %d, which draws %q", w, h.Kind, c, safeRune(row, c))
			}
		}
	}
}

// TestStripAndRailAgreeOnWhatPlusMeans is the two-widths rule: a control that
// means different things folded and unfolded is its own bug.
func TestStripAndRailAgreeOnWhatPlusMeans(t *testing.T) {
	m := daemonRailOS(t, 120, 40)
	m.sidebarPanelLines()
	var railKind sidebarRowKind = -1
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowNewSession {
			railKind = h.Kind
		}
	}
	if railKind != sidebarRowNewSession {
		t.Fatal("the expanded rail drew no new-session control")
	}

	m.SidebarCollapsed = true
	m.sidebarPanelLines()
	// Both controls are on the strip, because both lists are: a "+" at the head
	// of the sessions list makes a session and a "+" at the head of the
	// terminals list makes a pane, exactly as they do open. The strip used to
	// carry only the first, on the argument that a second one would point at a
	// list that was not on the screen; the list is on the screen now.
	for _, kind := range []sidebarRowKind{sidebarRowNewSession, sidebarRowNewWindow} {
		hit, ok := sidebarHitOfKind(m, kind)
		if !ok {
			t.Errorf("the collapsed strip records no %v target", kind)
			continue
		}
		label := ""
		for _, r := range m.sidebarStripRows {
			if r.Y0 == hit.Y0 {
				label = r.Label
			}
		}
		if label != sidebarAddWords(kind) {
			t.Errorf("the strip's %v says %q and the rail's says %q", kind, label, sidebarAddWords(kind))
		}
	}
}

// sidebarHitOfKind is the first recorded rectangle of a given kind.
func sidebarHitOfKind(m *OS, kind sidebarRowKind) (sidebarRowHit, bool) {
	for _, h := range m.SidebarHits {
		if h.Kind == kind {
			return h, true
		}
	}
	return sidebarRowHit{}, false
}

func safeRune(row []rune, i int) string {
	if i < 0 || i >= len(row) {
		return "<off the row>"
	}
	return string(row[i])
}
