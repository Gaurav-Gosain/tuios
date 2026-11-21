package app

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

type sidebarSplitState struct {
	Active  bool
	PressAt time.Time
}

type sidebarSplitGeom struct {
	Plans  []sidebarSectionPlan
	Rows   []int
	RowH   []int
	TallH  []int
	Pinned int
	Agents int
	Avail  int
	Bottom int
	Valid  bool
}

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

func (g sidebarSplitGeom) sidebarSplitShareFor(y int) int {
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

const sidebarSplitStep = 5

const (
	sidebarSplitMin = 5
	sidebarSplitMax = 95
)

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

func (m *OS) sidebarSplitEffective() int {
	if m.SidebarSectionSplit > 0 {
		return m.SidebarSectionSplit
	}
	plans := m.sidebarPlans()
	if !sidebarLayoutPins(plans) {
		return 0
	}
	return plans[len(plans)-1].Share
}

const sidebarDividerGrip = 3

func sidebarDividerGlyph(s *config.Settings) string {
	glyph := s.GetWindowBorderTop()
	if overlay.UseASCII() || lipgloss.Width(glyph) != 1 {
		return "-"
	}
	return glyph
}

func (m *OS) sidebarDividerRow(cw int, pal overlay.Palette, active bool) string {
	glyph := sidebarDividerGlyph(&m.Settings)
	if active {
		return sidebarFit(sidebarStyle(nil, nil).Render(" ")+
			sidebarStyle(nil, pal.FgDim).Render(strings.Repeat(glyph, max(cw-2, 1))), cw, nil)
	}
	grip := min(sidebarDividerGrip, max(cw-2, 1))
	lead := max((cw-grip)/2, 0)
	return sidebarFit(sidebarStyle(nil, nil).Render(strings.Repeat(" ", lead))+
		sidebarStyle(nil, m.railRule()).Render(strings.Repeat(glyph, grip)), cw, nil)
}

// SidebarSplitActive reports whether a divider drag is in progress, so the
// motion and release handlers route to the rail first.
func (m *OS) SidebarSplitActive() bool { return m.sidebarSplit.Active }

func (m *OS) sidebarSplitPress() {
	now := time.Now()
	if !m.sidebarSplit.PressAt.IsZero() && now.Sub(m.sidebarSplit.PressAt) <= m.DoubleClickWindow() {
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
	share := g.sidebarSplitShareFor(y - m.viewReserve().Top)
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
