package app

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The split between the sections above and the block the rail pins to its
// bottom edge used to be fixed arithmetic: the pinned section's share in
// appearance.sidebar.sections, and nothing on the rail to move it with. It is
// now a divider row, the line the pinned block already wore to float free of
// whatever ended up over it, drawn as a rule so it can be seen and grabbed.
//
// Dragging the divider sets the pinned section's share of the rail. The share
// is a ceiling, as every share is (see sidebar_layout.go), so the divider can
// only move up as far as the block has rows to fill: a block of two agents
// stays two rows tall wherever the divider is asked to go, and what the drag
// records is how much of the rail the block may take when it has more.
//
// The ratio persists in sidebar.json as section_split. Absent means the layout
// string's own share, which is what every rail did before the divider existed.
// A double-click on the divider, or enter with the cursor on it, puts it back.

// sidebarSplitState is the drag in progress on the divider, and the press that
// may turn out to be the first half of a double-click.
type sidebarSplitState struct {
	Active  bool
	PressAt time.Time
}

// sidebarSplitGeom is what the last frame wrote down about the divider so the
// drag can turn a pointer row into a share: the layout the frame budgeted,
// with its row counts and heights, the content lines it budgeted them over,
// and the rail-relative line the pinned block ends on.
//
// It carries the frame's inputs rather than one number because the allocator
// is not linear in the share. A share is a ceiling the block's rows cap, and
// an agents block that can afford a note line per row takes two lines a row
// and doubles what its share buys. A pointer row turned into a share by
// arithmetic alone therefore asked for sizes the block could not take, and
// the divider sat still under a moving pointer. So the drag asks the same
// allocator the frame used, share by share, which one lands the divider
// nearest the pointer. See sidebarSplitShareFor.
type sidebarSplitGeom struct {
	Plans  []sidebarSectionPlan
	Rows   []int
	RowH   []int
	TallH  []int // nil when the pinned block cannot go tall
	Pinned int   // index into Plans, or -1
	Agents int   // agent rows, for the tall test
	Avail  int
	Bottom int
	Valid  bool
}

// pinnedLines is how many lines the pinned block gets at this share, with the
// tall test the frame applies.
func (g sidebarSplitGeom) pinnedLines(share int) int {
	plans := make([]sidebarSectionPlan, len(g.Plans))
	copy(plans, g.Plans)
	plans[g.Pinned].Share = share
	if g.TallH != nil {
		if grown := sidebarBudgetLines(g.Avail, plans, g.Rows, g.TallH); grown[g.Pinned] >= g.Agents*sidebarAgentRowTall {
			return grown[g.Pinned]
		}
	}
	return sidebarBudgetLines(g.Avail, plans, g.Rows, g.RowH)[g.Pinned]
}

// sidebarSplitShareFor is the share that puts the divider on rail-relative
// row y, or as near it as the block's rows and floors allow. Among shares that
// land equally near, the one closest to the plain proportion wins, so a drag
// past the block's cap still records how much of the rail it asked for.
func (g sidebarSplitGeom) sidebarSplitShareFor(y int) int {
	// The divider on the pointer's row, the header under it, and the rows
	// under that down to the block's end.
	want := g.Bottom - y - 2
	linear := max(min(want*100/max(g.Avail, 1), sidebarSplitMax), sidebarSplitMin)
	if g.Pinned < 0 || g.Pinned >= len(g.Plans) {
		return linear
	}
	best, bestD := linear, abs(g.pinnedLines(linear)-want)
	for s := sidebarSplitMin; s <= sidebarSplitMax; s++ {
		d := abs(g.pinnedLines(s) - want)
		if d < bestD || (d == bestD && abs(s-linear) < abs(best-linear)) {
			best, bestD = s, d
		}
	}
	return best
}

// sidebarSplitDoubleClick is how long after a press on the divider a second
// press still reads as a double-click.
const sidebarSplitDoubleClick = 300 * time.Millisecond

// sidebarSplitStep is what one keypress moves the share by, in percent.
const sidebarSplitStep = 5

// Bounds on the share a drag or a key can set. Below the floor the block
// would be its floor rows whatever the number said; above the ceiling the
// sections over it would be at theirs.
const (
	sidebarSplitMin = 5
	sidebarSplitMax = 95
)

// sidebarApplySplit is the layout with the dragged share written over the
// pinned section's, on a copy so the parsed layout stays what config said.
func (m *OS) sidebarApplySplit(plans []sidebarSectionPlan, pinned sidebarSection) []sidebarSectionPlan {
	if m.SidebarSectionSplit <= 0 || pinned == sidebarSectionCount {
		return plans
	}
	out := make([]sidebarSectionPlan, len(plans))
	copy(out, plans)
	for i := range out {
		if !out[i].Spacer && out[i].Section == pinned {
			out[i].Share = m.SidebarSectionSplit
		}
	}
	return out
}

// sidebarSplitEffective is the share in force for the pinned section: the
// dragged one, or the layout's own.
func (m *OS) sidebarSplitEffective() int {
	if m.SidebarSectionSplit > 0 {
		return m.SidebarSectionSplit
	}
	plans := sidebarLayoutPlans(&m.Settings)
	if !sidebarLayoutPins(plans) {
		return 0
	}
	return plans[len(plans)-1].Share
}

// sidebarDividerGrip is how many cells the resting divider draws.
const sidebarDividerGrip = 3

// sidebarDividerGlyph is the cell the divider is drawn from: the border
// style's own horizontal, or a dash where the terminal cannot draw one.
func sidebarDividerGlyph(s *config.Settings) string {
	glyph := s.GetWindowBorderTop()
	if overlay.UseASCII() || lipgloss.Width(glyph) != 1 {
		return "-"
	}
	return glyph
}

// sidebarDividerRow draws the divider. At rest it is a short grip in the edge
// rule's own ink, centred on the line the pinned block already floated on: a
// full-width rule there was tried and taken out, because it was the third
// hairline on one screen beside the rail edge and the dock separator, and the
// gap separates as well while saying nothing. Three faint cells say "this can
// be held" without saying it loudly. Under the pointer, the cursor or a drag
// the grip opens out into the rule across the content columns in dim ink, so
// the thing being steered reads as the thing being steered.
func (m *OS) sidebarDividerRow(cw int, pal overlay.Palette, active bool) string {
	glyph := sidebarDividerGlyph(&m.Settings)
	if active {
		// One cell of air at each end, so the rule reads as a handle inside
		// the rail rather than as a border the rail grew.
		return sidebarFit(sidebarStyle(nil, nil).Render(" ")+
			sidebarStyle(nil, pal.FgDim).Render(strings.Repeat(glyph, max(cw-2, 1))), cw, nil)
	}
	grip := min(sidebarDividerGrip, max(cw-2, 1))
	lead := max((cw-grip)/2, 0)
	return sidebarFit(sidebarStyle(nil, nil).Render(strings.Repeat(" ", lead))+
		sidebarStyle(nil, theme.RailRule()).Render(strings.Repeat(glyph, grip)), cw, nil)
}

// SidebarSplitActive reports whether a divider drag is in progress, so the
// motion and release handlers route to the rail first.
func (m *OS) SidebarSplitActive() bool { return m.sidebarSplit.Active }

// sidebarSplitPress arms a drag on the divider, or resets the split when the
// press is the second half of a double-click.
func (m *OS) sidebarSplitPress() {
	now := time.Now()
	if !m.sidebarSplit.PressAt.IsZero() && now.Sub(m.sidebarSplit.PressAt) <= sidebarSplitDoubleClick {
		m.sidebarSplit = sidebarSplitState{}
		m.SidebarResetSplit()
		return
	}
	m.sidebarSplit = sidebarSplitState{Active: true, PressAt: now}
}

// SidebarSplitMotion moves the split to the pointer's row: the pinned block
// starts where the pointer is, and its share is what that leaves it.
func (m *OS) SidebarSplitMotion(x, y int) bool {
	if !m.sidebarSplit.Active {
		return false
	}
	g := m.sidebarSplitGeom
	if !g.Valid || g.Avail <= 0 {
		return true
	}
	share := g.sidebarSplitShareFor(y - m.GetTopMargin())
	if share != m.SidebarSectionSplit {
		m.SidebarSectionSplit = share
		m.MarkAllDirty()
	}
	return true
}

// SidebarSplitRelease ends the drag and persists the share.
func (m *OS) SidebarSplitRelease(x, y int) bool {
	if !m.sidebarSplit.Active {
		return false
	}
	m.sidebarSplit.Active = false
	m.saveSidebarState()
	return true
}

// SidebarSplitStep moves the split by one step in either direction: a
// positive delta gives the pinned block more of the rail.
func (m *OS) SidebarSplitStep(delta int) {
	share := m.sidebarSplitEffective() + delta*sidebarSplitStep
	m.SidebarSectionSplit = max(min(share, sidebarSplitMax), sidebarSplitMin)
	m.saveSidebarState()
	m.MarkAllDirty()
}

// SidebarResetSplit puts the split back on the layout's own share.
func (m *OS) SidebarResetSplit() {
	if m.SidebarSectionSplit == 0 {
		return
	}
	m.SidebarSectionSplit = 0
	m.saveSidebarState()
	m.MarkAllDirty()
}

// SidebarCursorOnDivider reports whether the rail's cursor is on the divider,
// which is when the narrow and widen keys move the split instead of the rail.
func (m *OS) SidebarCursorOnDivider() bool {
	row, ok := m.sidebarCursorRow()
	return ok && row.Kind == sidebarRowDivider
}
