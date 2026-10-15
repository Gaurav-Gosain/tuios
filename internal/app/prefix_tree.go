package app

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The work behind the actions that give tuios a tmux-shaped keymap to bind:
// marking and joining panes, reordering workspaces, the mouse and the config
// reload.

// MarkPane marks the focused pane for JoinMarkedPane, or clears the mark when
// the focused pane is the marked one: tmux's select-pane -m.
func (m *OS) MarkPane() {
	w := m.GetFocusedWindow()
	if w == nil {
		return
	}
	if m.MarkedPaneID == w.ID {
		m.setMarkedPane("")
		m.ShowNotification("Mark cleared", "info", m.Settings.NotificationDuration)
		return
	}
	m.setMarkedPane(w.ID)
	m.ShowNotification("Pane marked: "+w.Title(), "info", m.Settings.NotificationDuration)
}

// markedPaneBadge is what the title bar of the marked pane carries after its
// name, so the mark can be seen. It is text, not a colour: it reads on every
// theme and at 16 colours, and a test can find it on the screen.
const markedPaneBadge = "[marked]"

// setMarkedPane marks the pane with the given ID, or clears the mark for "".
// The title bar is part of the cached pane layer, so the pane that loses the
// mark and the pane that gets it are both redrawn.
func (m *OS) setMarkedPane(id string) {
	for _, w := range m.Windows {
		if w.ID == m.MarkedPaneID || w.ID == id {
			w.InvalidateCache()
		}
	}
	m.MarkedPaneID = id
}

// JoinMarkedPane moves the marked pane next to the focused one, on the side
// dir names, from wherever it is: the same workspace or another one. tmux's
// join-pane -s marked. It works on the BSP tree, the only layout with a side
// to put a pane on.
func (m *OS) JoinMarkedPane(dir layout.PreselectionDir) {
	focused := m.GetFocusedWindow()
	if focused == nil {
		return
	}
	markedIdx := -1
	for i, w := range m.Windows {
		if w.ID == m.MarkedPaneID {
			markedIdx = i
			break
		}
	}
	if m.MarkedPaneID == "" || markedIdx < 0 {
		m.setMarkedPane("")
		m.ShowNotification("No pane is marked. Mark a pane first.", "warn", m.Settings.NotificationDuration)
		return
	}
	marked := m.Windows[markedIdx]
	switch {
	case marked.ID == focused.ID:
		m.ShowNotification("The marked pane is this pane", "info", m.Settings.NotificationDuration)
		return
	case !m.AutoTiling || !m.UseBSPLayout || m.UseScrollingLayout:
		m.ShowNotification("Turn on BSP tiling to join a pane", "info", m.Settings.NotificationDuration)
		return
	case isScratch(marked) || isScratch(focused) || session.IsScratchWorkspace(m.CurrentWorkspace):
		m.ShowNotification("A scratch pane stays in its scratch group", "info", m.Settings.NotificationDuration)
		return
	case marked.IsFloating || focused.IsFloating || marked.Minimized:
		m.ShowNotification("You can join only a tiled pane", "info", m.Settings.NotificationDuration)
		return
	}

	markedInt, focusedInt := m.GetWindowIntID(marked.ID), m.GetWindowIntID(focused.ID)
	if oldWorkspace := marked.Workspace; oldWorkspace != m.CurrentWorkspace {
		// MoveWindowToWorkspace changes the pane's workspace and retiles this
		// one, but leaves the trees alone: the pane has to leave the old
		// workspace's tree here, or it would be in two of them.
		if old := m.WorkspaceTrees[oldWorkspace]; old != nil {
			old.RemoveWindow(markedInt)
			if old.IsEmpty() {
				m.WorkspaceTrees[oldWorkspace] = nil
			}
		}
		m.MoveWindowToWorkspace(markedIdx, m.CurrentWorkspace)
	}
	m.setMarkedPane("")
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree != nil && tree.HasWindow(focusedInt) {
		// The retile above may already have put the pane in the tree, in
		// whatever place it chose. It goes where the user asked instead.
		tree.RemoveWindow(markedInt)
		tree.InsertWindowWithPreselection(markedInt, focusedInt, dir, m.GetBSPBounds(), m.separatorGap())
	}
	// With no tree yet, the retile builds one with every pane, the joined one
	// included.
	m.TileAllWindows()
	m.reconcilePaneStreams()
	m.SyncStateToDaemon()
}

// MoveWorkspaceBy swaps the current workspace with the one delta places
// away and follows it, so the panes keep their layout and the workspace
// moves along the dock: tmux's swap-window -d -t -1 / +1.
func (m *OS) MoveWorkspaceBy(delta int) {
	from := m.CurrentWorkspace
	to := from + delta
	if session.IsScratchWorkspace(from) || to < 1 || to > m.NumWorkspaces {
		return
	}
	for _, w := range m.Windows {
		switch w.Workspace {
		case from:
			w.Workspace = to
		case to:
			w.Workspace = from
		default:
			continue
		}
		w.MarkPositionDirty()
	}
	m.swapPerWorkspaceState(from, to)
	// The panes on screen are the same ones, so this is not a switch: no
	// stream moves and nothing is retiled.
	m.CurrentWorkspace = to
	m.reconcilePaneStreams()
	m.SyncStateToDaemon()
	m.ShowNotification(fmt.Sprintf("Workspace %d moved to %d", from, to), "info", m.Settings.NotificationDuration)
}

// swapPerWorkspaceState swaps everything the client keeps per workspace
// number, so it goes with its panes when two workspaces trade places. A new
// map keyed by workspace must be added to this list, or a move would leave it
// describing the wrong workspace.
func (m *OS) swapPerWorkspaceState(a, b int) {
	swapWorkspaceKeys(m.WorkspaceTrees, a, b)
	swapWorkspaceKeys(m.WorkspaceFocus, a, b)
	swapWorkspaceKeys(m.FocusHistory, a, b)
	swapWorkspaceKeys(m.WorkspaceLayouts, a, b)
	swapWorkspaceKeys(m.WorkspaceHasCustom, a, b)
	swapWorkspaceKeys(m.WorkspaceMasterRatio, a, b)
	swapWorkspaceKeys(m.WorkspaceStackRatio, a, b)
	swapWorkspaceKeys(m.WorkspaceMasterSplits, a, b)
	swapWorkspaceKeys(m.WorkspaceNames, a, b)
	swapWorkspaceKeys(m.WorkspaceMasterLayout, a, b)
	swapWorkspaceKeys(m.masterSeeded, a, b)
	swapWorkspaceKeys(m.treeSeen, a, b)
	swapWorkspaceKeys(m.treeDerived, a, b)
	swapWorkspaceKeys(m.treeAnswerUser, a, b)
	swapWorkspaceKeys(m.paneRequests, a, b)
	swapWorkspaceKeys(m.WorkspaceScrollingLayouts, a, b)
	swapWorkspaceKeys(m.pendingScrollColumns, a, b)
}

// swapWorkspaceKeys swaps the entries of two workspaces in a per-workspace
// map, an absent entry included.
func swapWorkspaceKeys[V any](mp map[int]V, a, b int) {
	if mp == nil {
		return
	}
	va, okA := mp[a]
	vb, okB := mp[b]
	delete(mp, a)
	delete(mp, b)
	if okA {
		mp[b] = va
	}
	if okB {
		mp[a] = vb
	}
}

// NewWorkspaceTarget is the workspace new_workspace goes to: the one the dock's
// + button opens, so the key and the button agree. It is the lowest workspace
// with no pane that is not the one on screen, or 0 when every one has a pane.
func (m *OS) NewWorkspaceTarget() int { return m.nextFreeWorkspace() }

// ToggleMouse turns the client's mouse reporting off or back on.
func (m *OS) ToggleMouse() {
	m.MouseOff = !m.MouseOff
	if m.MouseOff {
		m.ShowNotification("Mouse off: the terminal selects text", "info", m.Settings.NotificationDuration)
	} else {
		m.ShowNotification("Mouse on", "info", m.Settings.NotificationDuration)
	}
}

// ReloadConfigFromDisk reads the config file again and applies it, saying
// whether it did. A config with an error is not applied.
func (m *OS) ReloadConfigFromDisk() tea.Cmd {
	configPath, err := config.GetConfigPath()
	if err != nil {
		m.ShowNotification("Config path error: "+err.Error(), "error", 0)
		return nil
	}
	newCfg, err := config.ReloadConfig(configPath)
	if err != nil {
		// The running config stands. Saying so is the point: a reload that
		// quietly did nothing reads as a reload that worked.
		m.ShowNotification("Config not reloaded: "+err.Error(), "error", 0)
		return nil
	}
	cmd := m.ApplyReloadedConfig(newCfg)
	m.ShowNotification("Config reloaded", "success", 0)
	return cmd
}
