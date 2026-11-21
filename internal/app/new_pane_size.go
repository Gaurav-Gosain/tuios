package app

import (
	"math"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func (m *OS) newPaneBox(placing *terminal.Window) (w, h int, tiled, ok bool) {
	if m.InScratchView() || m.daemonWindowIntent {
		return 0, 0, false, false
	}
	if !m.AutoTiling {
		_, _, w, h = m.NewWindowPlacement()
		return w, h, false, true
	}
	for _, win := range m.Windows {
		if win != nil && win.Workspace == m.CurrentWorkspace && win.Zoomed {
			return 0, 0, false, false
		}
	}
	tiled = m.panesBorderless()
	if m.UseScrollingLayout {
		if placing != nil {
			sl := m.WorkspaceScrollingLayouts[m.CurrentWorkspace]
			if id, mapped := m.WindowToBSPID[placing.ID]; mapped && sl != nil && sl.HasWindow(id) {
				preview := *sl
				m.applyScrollingGeometry(&preview)
				preview.ZoomedCol, preview.ZoomProportion = -1, 0
				r := preview.ComputePositions(m.ScrollingViewWidth(), m.PaneHeight(), m.PaneTop())[id]
				return r.W, r.H, tiled, r.W > 0 && r.H > 0
			}
			if sl == nil && len(m.pendingScrollColumns[m.CurrentWorkspace]) > 0 {
				return 0, 0, false, false
			}
		}
		sl := layout.NewScrollingLayout()
		m.applyScrollingGeometry(sl)
		sl.AddColumn(-1)
		r := sl.ComputePositions(m.ScrollingViewWidth(), m.PaneHeight(), m.PaneTop())[-1]
		return r.W, r.H, tiled, r.W > 0 && r.H > 0
	}
	if !m.UseBSPLayout {
		panes := m.tilablePanes(m.CurrentWorkspace)
		count, index := len(panes)+1, len(panes)
		if placing != nil {
			count, index = len(panes), slices.Index(panes, placing)
		}
		if index < 0 {
			return 0, 0, false, false
		}
		r := m.contentTileLayouts(count)[index]
		return r.Width, r.Height, tiled, r.Width > 0 && r.Height > 0
	}

	bounds := m.GetBSPBounds()
	gap := m.separatorGap()
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil || tree.IsEmpty() {
		return bounds.W, bounds.H, tiled, bounds.W > 0 && bounds.H > 0
	}

	targetID, dir := m.SplitTargetWindowID, m.PreselectionDir
	if m.pendingSplitTarget != "" {
		targetID, dir = m.pendingSplitTarget, m.pendingSplitDir
	}
	if targetID == "" {
		if fw := m.GetFocusedWindow(); fw != nil {
			targetID = fw.ID
		}
	}
	trial := tree.Serialize().Deserialize()
	const probe = math.MaxInt32
	target := m.bspSplitTarget(trial, targetID)
	if dir != layout.PreselectionNone {
		trial.InsertWindowWithPreselection(probe, target, dir, bounds, gap)
	} else {
		trial.InsertWindow(probe, target, layout.SplitNone, 0.5, bounds, gap)
	}
	rect, found := trial.ApplyLayout(bounds, gap)[probe]
	if !found || rect.W <= 0 || rect.H <= 0 {
		return 0, 0, false, false
	}
	return rect.W, rect.H, tiled, true
}

func (m *OS) newPaneContentSize() (cols, rows int, ok bool) {
	w, h, tiled, ok := m.newPaneBox(nil)
	if !ok {
		return 0, 0, false
	}
	cols, rows = terminal.ContentSize(w, h, tiled)
	return cols, rows, true
}
