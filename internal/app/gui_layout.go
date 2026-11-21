package app

import (
	"errors"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

var errNotTiledTree = errors.New("the workspace is not tiled with splits")

func (m *OS) treeEditTarget() (*layout.BSPTree, error) {
	if m.LayoutName() != LayoutModeBSP {
		return nil, errNotTiledTree
	}
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil || tree.IsEmpty() {
		return nil, errNotTiledTree
	}
	m.requireRealLayout()
	return tree, nil
}

func (m *OS) tiledLeaf(tree *layout.BSPTree, id string) (*terminal.Window, int, error) {
	w := m.windowByID(id)
	if w == nil {
		return nil, 0, errfmt.Errorf("no pane %s", id)
	}
	if w.Workspace != m.CurrentWorkspace {
		return nil, 0, errfmt.Errorf("pane %s is on workspace %d, not on the workspace on screen", id, w.Workspace)
	}
	n := m.GetWindowIntID(id)
	if !tree.HasWindow(n) {
		return nil, 0, errfmt.Errorf("pane %s is not tiled", id)
	}
	return w, n, nil
}

func (m *OS) finishTreeEdit() {
	m.ApplyBSPLayout()
	m.MarkAllDirty()
	m.SyncStateToDaemon()
}

// MovePaneBeside takes the pane id out of its place and puts it beside the
// pane target, on side, in a new split at the tree's default ratio. The space
// it leaves goes to its sibling. Focus goes to the pane that moved.
func (m *OS) MovePaneBeside(id, target string, side layout.Side) error {
	tree, err := m.treeEditTarget()
	if err != nil {
		return err
	}
	_, n, err := m.tiledLeaf(tree, id)
	if err != nil {
		return err
	}
	_, tn, err := m.tiledLeaf(tree, target)
	if err != nil {
		return err
	}
	if n == tn {
		return errfmt.Errorf("pane %s cannot move beside itself", id)
	}
	tree.MoveWindow(n, tn, side.Preselection())
	_ = m.FocusWindowByID(id)
	m.finishTreeEdit()
	return nil
}

// MovePaneToRoot takes the pane id out of its place and puts it on side of
// everything else, in a new root split of 0.5. Focus goes to the pane.
func (m *OS) MovePaneToRoot(id string, side layout.Side) error {
	tree, err := m.treeEditTarget()
	if err != nil {
		return err
	}
	_, n, err := m.tiledLeaf(tree, id)
	if err != nil {
		return err
	}
	if !tree.MoveWindowToRoot(n, side) {
		return errfmt.Errorf("pane %s is the only tiled pane", id)
	}
	_ = m.FocusWindowByID(id)
	m.finishTreeEdit()
	return nil
}

// SetSplitRatio sets one split of the tree on screen, named by its node ID,
// and returns the ratio it took after the minimum pane size held it.
func (m *OS) SetSplitRatio(split uint64, ratio float64) (float64, error) {
	tree, err := m.treeEditTarget()
	if err != nil {
		return 0, err
	}
	got, ok := tree.SetSplitRatio(split, ratio, m.GetBSPBounds(), m.separatorGap())
	if !ok {
		return 0, errfmt.Errorf("no split %d on the workspace on screen", split)
	}
	m.finishTreeEdit()
	return got, nil
}

// SetFloatingGeometry moves and sizes a floating pane, in cells. The box is
// held to at least the smallest pane and kept on the screen.
func (m *OS) SetFloatingGeometry(id string, x, y, width, height int) error {
	w := m.windowByID(id)
	if w == nil {
		return errfmt.Errorf("no pane %s", id)
	}
	if m.AutoTiling && !w.IsFloating {
		return errfmt.Errorf("pane %s is tiled, not floating", id)
	}
	width = max(width, config.DefaultWindowWidth)
	height = max(height, config.DefaultWindowHeight)
	m.CancelAnimationsForWindow(w)
	w.X, w.Y = x, y
	w.Resize(width, height)
	w.MarkPositionDirty()
	w.InvalidateCache()
	m.ClampWindowsToView()
	m.MarkAllDirty()
	m.SyncStateToDaemon()
	return nil
}

// ToggleFloatingByID floats a tiled pane or tiles a floating one, as the
// toggle key does for the focused pane. The pane takes the focus.
func (m *OS) ToggleFloatingByID(id string) error {
	w := m.windowByID(id)
	if w == nil {
		return errfmt.Errorf("no pane %s", id)
	}
	if w.IsPopup || w.IsScratch {
		return errfmt.Errorf("pane %s is a popup or a scratch pane, and always floats", id)
	}
	if w.Workspace != m.CurrentWorkspace {
		return errfmt.Errorf("pane %s is on workspace %d, not on the workspace on screen", id, w.Workspace)
	}
	m.requireRealLayout()
	_ = m.FocusWindowByID(id)
	m.ToggleFloating()
	m.MarkAllDirty()
	m.SyncStateToDaemon()
	return nil
}

// RunActionCmd runs a keybinding action by name, as RunAction does, and
// returns the command it left to run, for a host that runs its own Update
// loop. The result goes to the daemon.
func (m *OS) RunActionCmd(name string) (tea.Cmd, error) {
	if err := m.RunAction(name); err != nil {
		return nil, err
	}
	cmd := m.takeScriptCmds()
	if m.IsDaemonSession {
		m.SyncStateToDaemon()
	}
	return cmd, nil
}

// SessionView is how this client shows a session that is a different size
// from it (see pane_view.go).
type SessionView struct {
	// Cols and Rows are the session's size, the box every pane is laid out
	// in. Until the daemon names a size, it is this client's own.
	Cols, Rows int
	// ClientCols and ClientRows are this client's own size.
	ClientCols, ClientRows int
	// Cropped is set when the session is larger than this client, which then
	// shows the part of it around the focused pane's cursor. OffsetX and
	// OffsetY are where that part starts, in the session's cells.
	Cropped          bool
	OffsetX, OffsetY int
}

// CurrentSessionView reports the session's size against this client's, and
// the view's offset when the session is the larger.
func (m *OS) CurrentSessionView() SessionView {
	v := SessionView{
		Cols: m.GetLayoutWidth(), Rows: m.GetLayoutHeight(),
		ClientCols: m.Width, ClientRows: m.Height,
	}
	if sv := m.computeSessionView(); sv.on && m.ViewCropped() {
		v.Cropped = true
		v.OffsetX, v.OffsetY = sv.offX, sv.offY
	}
	return v
}

// AgentsSeen reports whether an agent has been seen in this session, the rule
// that turns the agent chrome on (see agents_seen.go).
func (m *OS) AgentsSeen() bool { return m.agentsSeen() }
