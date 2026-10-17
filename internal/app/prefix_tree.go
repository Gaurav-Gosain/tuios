package app

import (
	"fmt"
	"slices"

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
// moves along the dock: tmux's swap-window -d -t -1 / +1. Both workspaces
// are renumbered, so the moved one answers to its new number.
func (m *OS) MoveWorkspaceBy(delta int) tea.Cmd {
	from := m.CurrentWorkspace
	to := from + delta
	if session.IsScratchWorkspace(from) || to < 1 || to > m.NumWorkspaces {
		return nil
	}
	// Past the last workspace there is nothing to swap with: a daemon session
	// numbers its workspaces 1 to their count.
	if m.IsDaemonSession && !slices.Contains(m.occupiedWorkspaceNumbers(), to) {
		return nil
	}
	// The order is the two workspaces as they will stand, lower number first.
	order := []int{to, from}
	if delta < 0 {
		order = []int{from, to}
	}
	cmd := m.RenumberWorkspaces(order)
	if cmd != nil || !m.IsDaemonSession {
		m.ShowNotification(fmt.Sprintf("Workspace %d moved to %d", from, to), "info", m.Settings.NotificationDuration)
	}
	return cmd
}

// RenumberWorkspaces puts the workspaces named in order into that order: they
// take the numbers they hold between them, smallest first (see
// session.SessionState.renumberWorkspaces). A daemon session asks the daemon,
// which renumbers the session for every client at once, and this client takes
// the move from the state it publishes (see adoptWorkspaceRenumbering). A
// session with no daemon renumbers here.
//
// One renumber is asked for at a time. The order names workspaces by the
// numbers this client shows, and until the daemon's renumber arrives those are
// the numbers from before it: a second move sent in that gap would name the
// wrong workspaces, so it is dropped, and the user moves again once the first
// one has landed.
func (m *OS) RenumberWorkspaces(order []int) tea.Cmd {
	if m.IsDaemonSession && m.DaemonClient != nil {
		if m.workspaceMoveInFlight {
			return nil
		}
		cmd := setWorkspaceOrderCmd(m.verbDialer(), m.SessionName, order)
		m.workspaceMoveInFlight = cmd != nil
		return cmd
	}
	m.renumberLocalWorkspaces(workspaceSlots(order))
	return nil
}

// workspaceSlots is where each workspace in order goes: the workspaces named
// take the numbers they hold between them, smallest first, in that order.
func workspaceSlots(order []int) map[int]int {
	slots := slices.Sorted(slices.Values(order))
	newOf := make(map[int]int, len(order))
	for i, ws := range order {
		if slots[i] != ws {
			newOf[ws] = slots[i]
		}
	}
	return newOf
}

// adoptWorkspaceRenumbering applies, to what this client keeps by workspace
// number, a renumber the daemon made: it reads where each workspace went from
// the herdr tab keys, which follow a workspace through a move, so a state that
// arrives after several renumbers still says where each one ended up. It runs
// before the rest of the state is applied, which then finds every window
// already on its new workspace.
func (m *OS) adoptWorkspaceRenumbering(state *session.SessionState) {
	if !m.workspaceTabKeysKnown {
		return
	}
	newOf := session.WorkspaceRenumbering(m.workspaceTabKeys, state)
	if newOf == nil {
		return
	}
	m.renumberLocalWorkspaces(newOf)
	m.pendingWorkspaceOrder = nil
	m.workspaceMoveInFlight = false
}

// renumberLocalWorkspaces moves everything the client keeps per workspace
// number to the workspace's new number. A new map or field keyed by workspace
// must be added to this list, or a move would leave it describing the wrong
// workspace.
func (m *OS) renumberLocalWorkspaces(newOf map[int]int) {
	if len(newOf) == 0 {
		return
	}
	to := func(ws int) int {
		if n, ok := newOf[ws]; ok {
			return n
		}
		return ws
	}
	for _, w := range m.Windows {
		if n := to(w.Workspace); n != w.Workspace {
			w.Workspace = n
			w.MarkPositionDirty()
		}
	}
	// The panes on screen are the same ones, so this is not a switch: no
	// stream moves and nothing is retiled.
	m.CurrentWorkspace = to(m.CurrentWorkspace)
	m.lastWorkspace = to(m.lastWorkspace)
	m.scratchBase = to(m.scratchBase)
	m.SessionCloseWorkspace = to(m.SessionCloseWorkspace)
	m.menuWorkspace = to(m.menuWorkspace)
	m.WorkspaceTrees = session.RenumberWorkspaceMap(m.WorkspaceTrees, newOf)
	m.WorkspaceFocus = session.RenumberWorkspaceMap(m.WorkspaceFocus, newOf)
	m.FocusHistory = session.RenumberWorkspaceMap(m.FocusHistory, newOf)
	m.WorkspaceLayouts = session.RenumberWorkspaceMap(m.WorkspaceLayouts, newOf)
	m.WorkspaceHasCustom = session.RenumberWorkspaceMap(m.WorkspaceHasCustom, newOf)
	m.WorkspaceMasterRatio = session.RenumberWorkspaceMap(m.WorkspaceMasterRatio, newOf)
	m.WorkspaceStackRatio = session.RenumberWorkspaceMap(m.WorkspaceStackRatio, newOf)
	m.WorkspaceMasterSplits = session.RenumberWorkspaceMap(m.WorkspaceMasterSplits, newOf)
	m.WorkspaceNames = session.RenumberWorkspaceMap(m.WorkspaceNames, newOf)
	m.WorkspaceMasterLayout = session.RenumberWorkspaceMap(m.WorkspaceMasterLayout, newOf)
	m.masterSeeded = session.RenumberWorkspaceMap(m.masterSeeded, newOf)
	m.treeSeen = session.RenumberWorkspaceMap(m.treeSeen, newOf)
	m.treeDerived = session.RenumberWorkspaceMap(m.treeDerived, newOf)
	m.treeAnswerUser = session.RenumberWorkspaceMap(m.treeAnswerUser, newOf)
	m.paneRequests = session.RenumberWorkspaceMap(m.paneRequests, newOf)
	m.WorkspaceScrollingLayouts = session.RenumberWorkspaceMap(m.WorkspaceScrollingLayouts, newOf)
	m.pendingScrollColumns = session.RenumberWorkspaceMap(m.pendingScrollColumns, newOf)
	m.workspaceMarkSet = session.RenumberWorkspaceMap(m.workspaceMarkSet, newOf)
	m.reconcilePaneStreams()
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
