package app

import (
	"math"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// newPaneBox is the outer box a pane will be placed in, and whether it is a
// borderless tile. placing is nil before creation, or the pane already in
// m.Windows when a daemon state is being placed. ok is false when this client
// cannot tell; the pane is then created at the old size and resized once placed.
//
// It exists for the process in the pane. A shell started at one size and
// resized a moment later takes a SIGWINCH while it is still starting, and zsh
// answers that by drawing its prompt again under PROMPT_SP's mark: empty rows
// and a "%" above the prompt in every new pane (#1205). A process started at
// the size its pane is placed at is never resized while it starts.
//
// It covers floating panes and the stock tilers. A zoom, a scratch group or
// a custom layout places a pane by rules this does not repeat.
func (m *OS) newPaneBox(placing *terminal.Window) (w, h int, tiled, ok bool) {
	if m.InScratchView() || m.daemonWindowIntent || m.WorkspaceHasCustom[m.CurrentWorkspace] {
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
				// A sync can bring an existing column or a stacked pane. Its
				// box is the adopted strip's, not a fresh default-width column.
				preview := *sl
				preview.Gap = m.separatorGap()
				preview.DefaultWidth = m.ScrollColumnWidthFraction()
				preview.MaxProportion = float64(m.Settings.GetScrollColumnMax()) / 100
				preview.ZoomedCol, preview.ZoomProportion = -1, 0
				r := preview.ComputePositions(m.ScrollingViewWidth(), m.PaneHeight(), m.PaneTop())[id]
				return r.W, r.H, tiled, r.W > 0 && r.H > 0
			}
			if sl == nil && len(m.pendingScrollColumns[m.CurrentWorkspace]) > 0 {
				return 0, 0, false, false
			}
		}
		// Each new pane starts a new column; its size does not depend on
		// where that column is inserted in the strip.
		sl := layout.NewScrollingLayout()
		sl.DefaultWidth = m.ScrollColumnWidthFraction()
		sl.MaxProportion = float64(m.Settings.GetScrollColumnMax()) / 100
		sl.Gap = m.separatorGap()
		sl.AddColumn(-1)
		r := sl.ComputePositions(m.ScrollingViewWidth(), m.PaneHeight(), m.PaneTop())[-1]
		return r.W, r.H, tiled, r.W > 0 && r.H > 0
	}
	if !m.UseBSPLayout {
		panes := m.tilablePanes(m.CurrentWorkspace)
		count, index := len(panes)+1, len(panes)
		if placing != nil {
			// A creating sync has already appended this pane. Count it once
			// and use its own slot, also when several unplaced panes arrive.
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
		// The first pane of a workspace takes the whole box.
		return bounds.W, bounds.H, tiled, bounds.W > 0 && bounds.H > 0
	}

	// The same insertion the pane gets when it arrives, on a copy of the tree.
	// The target and the direction are read the way the two insertion paths
	// read them: AddWindowToBSPTree here, insertSyncedWindow in a daemon
	// session, where addDaemonWindow has already moved a preselection into
	// pendingSplitDir.
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
	const probe = math.MaxInt32 // an int ID no window has
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

// newPaneContentSize is the content size of the pane newPaneBox places: the
// size its process should start at.
func (m *OS) newPaneContentSize() (cols, rows int, ok bool) {
	w, h, tiled, ok := m.newPaneBox(nil)
	if !ok {
		return 0, 0, false
	}
	cols, rows = terminal.ContentSize(w, h, tiled)
	return cols, rows, true
}
