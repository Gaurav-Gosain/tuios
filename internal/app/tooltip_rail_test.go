package app

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// tooltipOS renders a collapsed strip once so the strip rows exist, then hands
// back the y of the row of the given kind.
func tooltipOS(t *testing.T, pos string, kind sidebarStripRowKind) (*OS, int) {
	t.Helper()
	m, tree := stripOS(t, 120, 20)
	withSidebar(t, true, pos, config.SidebarDefaultWidth)
	m.SidebarCollapsed = true
	m.sidebarPanelLinesForTree(tree)
	for _, r := range m.sidebarStripRows {
		if r.Kind == kind {
			return m, r.Y
		}
	}
	t.Fatalf("the strip drew no row of kind %v", kind)
	return nil, 0
}

// TestTooltipPendsOnlyDuringALiveHover is the tick-idle guarantee. A leaked
// pending flag ticks forever, which is the one thing this feature is not
// allowed to cost.
func TestTooltipPendsOnlyDuringALiveHover(t *testing.T) {
	m, y := tooltipOS(t, "left", sidebarStripSession)

	if m.TooltipPending() {
		t.Fatal("a rail nobody is pointing at is pending a tooltip")
	}
	m.SidebarMotion(1, y)
	if !m.TooltipPending() {
		t.Fatal("landing on a strip row did not arm the tooltip")
	}
	if m.tooltipVisible(tooltipRailStrip) {
		t.Error("the tooltip appeared before its delay elapsed")
	}
	if !m.tickNeedsWork() {
		t.Error("a pending tooltip does not hold the maintenance tick, so nothing will draw it")
	}

	// The delay elapses and the frame that draws it closes the gate.
	m.Tooltip.At = time.Now().Add(-2 * tooltipDelay)
	if !m.tooltipVisible(tooltipRailStrip) {
		t.Fatal("the tooltip never became visible")
	}
	if m.renderRailTooltip() == nil {
		t.Fatal("a visible tooltip composed no layer")
	}
	if m.TooltipPending() {
		t.Error("a shown tooltip is still pending; the tick would never idle")
	}

	// And leaving the band takes it down entirely.
	m.SidebarMotion(m.GetRenderWidth()-2, y)
	if m.TooltipPending() || m.tooltipVisible(tooltipRailStrip) {
		t.Error("leaving the band left the tooltip behind")
	}
}

// TestTooltipPendingClosesEvenWhenTheRowHasNothingToSay: the gate is closed by
// the drawing frame, not by the label, or a silent row would hold the tick open
// for as long as the pointer sat on it.
func TestTooltipPendingClosesOnASilentRow(t *testing.T) {
	m, y := tooltipOS(t, "left", sidebarStripBadge)
	m.SidebarMotion(1, y)
	// A row the renderer had nothing to say about.
	for i := range m.sidebarStripRows {
		m.sidebarStripRows[i].Label = ""
	}
	m.Tooltip.At = time.Now().Add(-2 * tooltipDelay)

	if m.renderRailTooltip() != nil {
		t.Fatal("a row with no label drew one anyway")
	}
	if m.TooltipPending() {
		t.Error("a row with nothing to say left the tooltip pending forever")
	}
}

// TestTooltipSwapsInstantlyOnceWarm: the delay is there to stop a crossing from
// popping labels, not to make browsing the strip slow.
func TestTooltipSwapsInstantlyOnceWarm(t *testing.T) {
	m, _ := tooltipOS(t, "left", sidebarStripSession)
	var rows []int
	for _, r := range m.sidebarStripRows {
		if r.Kind == sidebarStripSession {
			rows = append(rows, r.Y)
		}
	}
	if len(rows) < 2 {
		t.Fatal("the fixture needs two session rows")
	}

	m.SidebarMotion(1, rows[0])
	m.Tooltip.At = time.Now().Add(-2 * tooltipDelay)
	m.renderRailTooltip()

	m.SidebarMotion(1, rows[1])
	if !m.tooltipVisible(tooltipRailStrip) {
		t.Error("moving along a strip that already has a label waited the delay out again")
	}
	if m.Tooltip.Key != rows[1] {
		t.Errorf("the label stayed on row %d after the pointer moved to %d", m.Tooltip.Key, rows[1])
	}
}

// TestTooltipContentNamesTheRow: two cells cannot say any of this, which is the
// only reason the label exists.
func TestTooltipContentNamesTheRow(t *testing.T) {
	m, tree := stripOS(t, 120, 20)
	m.SidebarCollapsed = true
	m.sidebarPanelLinesForTree(tree)

	for _, r := range m.sidebarStripRows {
		got := r.Label
		switch r.Kind {
		case sidebarStripToggle:
			if got != "expand" {
				t.Errorf("the toggle's label is %q, want expand", got)
			}
		case sidebarStripBadge:
			if !strings.Contains(got, "2 agents") {
				t.Errorf("the badge's label is %q, want it to count the agents", got)
			}
		case sidebarStripSession:
			if !strings.Contains(got, "terminal") {
				t.Errorf("a session label is %q, want a pane count", got)
			}
			// api holds an errored pane and a blocked one, so its roll-up is the
			// louder of the two and the label says that, with its age.
			if r.SessionID == "api" && !strings.Contains(got, "errored") {
				t.Errorf("a loud session's label is %q, want it to say what is loud", got)
			}
		}
	}
}

// TestTooltipAnchorFlipsWithThePosition: it opens away from the rail, so it
// never covers the cell it is describing.
func TestTooltipAnchorFlipsWithThePosition(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		m, y := tooltipOS(t, pos, sidebarStripSession)
		m.SidebarMotion(m.GetSidebarWidth()/2+railOriginX(m), y)
		m.Tooltip.At = time.Now().Add(-2 * tooltipDelay)
		layer := m.renderRailTooltip()
		if layer == nil {
			t.Fatalf("%s: no tooltip layer", pos)
		}
		railW := m.GetSidebarWidth()
		width := lipgloss.Width(layer.GetContent())
		if pos == "left" {
			if layer.GetX() != railW {
				t.Errorf("left rail: tooltip at x=%d, want it flush at %d", layer.GetX(), railW)
			}
		} else {
			right := layer.GetX() + width
			if want := m.GetRenderWidth() - railW; right != want {
				t.Errorf("right rail: tooltip ends at x=%d, want it flush at %d", right, want)
			}
		}
		if layer.GetY() != y {
			t.Errorf("%s: tooltip at y=%d, want the hovered row %d", pos, layer.GetY(), y)
		}
	}
}

// TestTooltipClampsToThePaneArea: a long session name truncates rather than
// running off the screen.
func TestTooltipClampsToThePaneArea(t *testing.T) {
	m, _ := stripOS(t, 62, 20)
	m.SidebarCollapsed = true
	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", DisplayName: strings.Repeat("very-long-session-name-", 8), Attached: true, IsCurrent: true,
			Windows: []sessiontree.WindowInput{{ID: "aaaaaaaa1111", Title: "nvim", Focused: true}}},
	})
	m.sidebarPanelLinesForTree(tree)

	y := -1
	for _, r := range m.sidebarStripRows {
		if r.Kind == sidebarStripSession {
			y = r.Y
		}
	}
	if y < 0 {
		t.Fatal("the strip drew no session row")
	}
	m.SidebarMotion(1, y)
	m.Tooltip.At = time.Now().Add(-2 * tooltipDelay)

	layer := m.renderRailTooltip()
	if layer == nil {
		t.Fatal("no tooltip layer")
	}
	if got, limit := layer.GetX()+lipgloss.Width(layer.GetContent()), m.GetRenderWidth(); got > limit {
		t.Errorf("the tooltip runs to x=%d past the screen at %d", got, limit)
	}
}

// TestTooltipsCanBeTurnedOff: the config key has to reach the render, not just
// the struct.
func TestTooltipsCanBeTurnedOff(t *testing.T) {
	prev := config.Tooltips
	config.Tooltips = false
	t.Cleanup(func() { config.Tooltips = prev })

	m, y := tooltipOS(t, "left", sidebarStripSession)
	m.SidebarMotion(1, y)
	if m.TooltipPending() {
		t.Error("tooltips are off and one is pending anyway")
	}
	m.Tooltip.At = time.Now().Add(-2 * tooltipDelay)
	if m.renderRailTooltip() != nil {
		t.Error("tooltips are off and one drew anyway")
	}
}

// TestTooltipIsStripOnly: the expanded rail says everything the label says, so
// popping one over it would be noise on top of the answer.
func TestTooltipIsStripOnly(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.sidebarPanelLinesForTree(tree)
	m.SidebarMotion(1, m.GetTopMargin()+1)
	if m.TooltipPending() || m.renderRailTooltip() != nil {
		t.Error("the expanded rail popped a tooltip")
	}
}

// railOriginX is the rail's first screen column, which is where a test has to
// aim to land inside the band on either side.
func railOriginX(m *OS) int {
	if config.SidebarPosition == "right" {
		return m.GetRenderWidth() - m.GetSidebarWidth()
	}
	return 0
}
