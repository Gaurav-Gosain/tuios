package app

import (
	"math"

	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// newPaneBox is the outer box a pane about to be created will be placed in,
// and whether it is a borderless tile. ok is false when this client cannot
// tell before the pane exists; the pane is then created at the old size and
// resized once it is placed.
//
// It exists for the process in the pane. A shell started at one size and
// resized a moment later takes a SIGWINCH while it is still starting, and zsh
// answers that by drawing its prompt again under PROMPT_SP's mark: empty rows
// and a "%" above the prompt in every new pane (#1205). A process started at
// the size its pane is placed at is never resized while it starts.
//
// It covers floating panes and the split tree, which is where new panes go
// unless the person chose another tiler. A zoom, a scratch group, master-stack
// and the scrolling layout place a pane by rules this does not repeat.
func (m *OS) newPaneBox() (w, h int, tiled, ok bool) {
	if m.InScratchView() {
		return 0, 0, false, false
	}
	if !m.AutoTiling {
		_, _, w, h = m.NewWindowPlacement()
		return w, h, false, true
	}
	if m.UseScrollingLayout || !m.UseBSPLayout {
		return 0, 0, false, false
	}
	for _, win := range m.Windows {
		if win != nil && win.Workspace == m.CurrentWorkspace && win.Zoomed {
			return 0, 0, false, false
		}
	}

	bounds := m.GetBSPBounds()
	gap := m.separatorGap()
	tiled = m.panesBorderless()
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
	w, h, tiled, ok := m.newPaneBox()
	if !ok {
		return 0, 0, false
	}
	cols, rows = terminal.ContentSize(w, h, tiled)
	return cols, rows, true
}
