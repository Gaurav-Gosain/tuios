package app

import (
	"strconv"
	"strings"
)

// WorkspaceItem is one row of the workspace switcher. Number is the workspace's
// identity and the label it shows when it has no name, so a row for an unnamed
// workspace reads exactly as the dock's chip for it always has.
type WorkspaceItem struct {
	Number    int
	Name      string
	Panes     int
	IsCurrent bool
}

// Label is what the row shows: the workspace's name when it has one, otherwise
// its number. Never use it to address the workspace.
func (w WorkspaceItem) Label() string {
	if w.Name != "" {
		return w.Name
	}
	return strconv.Itoa(w.Number)
}

// buildWorkspaceItems lists the workspaces worth switching to: the ones
// holding a pane, the current one, and any the user has named. A named but
// empty workspace is listed because naming it is what says it is wanted.
//
// The rows are in the order the dock's pills are in. A switcher listing the same
// workspaces in a different order would be a second arrangement for the user to
// hold in their head, which is the thing one display order exists to prevent.
func (m *OS) buildWorkspaceItems() []WorkspaceItem {
	worth := make([]int, 0, m.NumWorkspaces)
	for n := 1; n <= m.NumWorkspaces; n++ {
		if m.GetWorkspaceWindowCount(n) > 0 || m.WorkspaceNames[n] != "" || n == m.CurrentWorkspace {
			worth = append(worth, n)
		}
	}
	items := make([]WorkspaceItem, 0, len(worth))
	for _, n := range m.workspaceDisplayOrder(worth) {
		items = append(items, WorkspaceItem{
			Number:    n,
			Name:      m.WorkspaceNames[n],
			Panes:     m.GetWorkspaceWindowCount(n),
			IsCurrent: n == m.CurrentWorkspace,
		})
	}
	return items
}

// OpenWorkspaceSwitcher shows the workspace switcher for the attached session.
// It reads live state only, so there is no round trip to pay here.
func (m *OS) OpenWorkspaceSwitcher() {
	m.ShowWorkspaceSwitcher = true
	m.WorkspaceSwitcherQuery = ""
	m.WorkspaceSwitcherScroll = 0
	m.WorkspaceSwitcherItems = m.buildWorkspaceItems()

	// Open on the workspace the user is standing in, so Enter is a no-op and
	// the arrows move away from a known place.
	m.WorkspaceSwitcherSelected = 0
	for i, w := range m.WorkspaceSwitcherItems {
		if w.IsCurrent {
			m.WorkspaceSwitcherSelected = i
			break
		}
	}
}

// CloseWorkspaceSwitcher hides the switcher and resets its transient state.
func (m *OS) CloseWorkspaceSwitcher() {
	m.ShowWorkspaceSwitcher = false
	m.WorkspaceSwitcherQuery = ""
	m.WorkspaceSwitcherSelected = 0
	m.WorkspaceSwitcherScroll = 0
}

// FilterWorkspaceItems narrows the list by a query, matching the name and the
// number. The number is matched because it is the identity and stays the way to
// reach a workspace whatever it has been called.
func FilterWorkspaceItems(items []WorkspaceItem, query string) []WorkspaceItem {
	if query == "" {
		return items
	}
	q := strings.ToLower(query)
	var filtered []WorkspaceItem
	for _, w := range items {
		if strings.Contains(strings.ToLower(w.Name), q) || strings.Contains(strconv.Itoa(w.Number), q) {
			filtered = append(filtered, w)
		}
	}
	return filtered
}

// WorkspaceSwitcherTarget resolves a row index against the FILTERED list, which
// is the only list the index can mean: with a query typed, row n on screen is
// not item n. Every activation path goes through it.
func (m *OS) WorkspaceSwitcherTarget(idx int) (WorkspaceItem, bool) {
	filtered := FilterWorkspaceItems(m.WorkspaceSwitcherItems, m.WorkspaceSwitcherQuery)
	if idx < 0 || idx >= len(filtered) {
		return WorkspaceItem{}, false
	}
	return filtered[idx], true
}

// WorkspaceSwitcherMove steps the selection by delta over count rows, keeping
// it in view by the same page the renderer draws.
func (m *OS) WorkspaceSwitcherMove(delta, count int) {
	moveListSelection(&m.WorkspaceSwitcherSelected, &m.WorkspaceSwitcherScroll, count, workspaceSwitcherRows, delta)
}

// WorkspaceSwitcherActivate switches to the workspace at the given row of the
// filtered list and closes the switcher.
//
// The Enter binding and the mouse click both come here rather than each writing
// the same three lines, which is how the two came to be fixed one at a time.
func (m *OS) WorkspaceSwitcherActivate(idx int) {
	target, ok := m.WorkspaceSwitcherTarget(idx)
	if !ok {
		// Nothing to switch to, so the switcher stays up: the query that
		// narrowed it to nothing is what the user is typing, and dismissing it
		// answers the key with silence.
		m.ShowNotification("Nothing to switch to: no workspace matches "+m.WorkspaceSwitcherQuery, "info", m.Settings.NotificationDuration)
		return
	}
	if !target.IsCurrent {
		m.SwitchToWorkspace(target.Number)
	}
	m.closeOverlay("workspace")
}
