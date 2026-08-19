package app

import (
	"path"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// windowRowTitle is the label a session-management surface shows for a window.
func windowRowTitle(w *terminal.Window) string {
	return railWindowLabel(w.CustomName, w.ForegroundCmd, w.Title())
}

// railWindowLabel is that label built from the pieces every surface has, live
// window or wire summary. The user's name wins; then what the pane is running,
// which is the one part that differs between siblings; then the shell's own
// title, shortened, because in full it is "<cwd> - <shell>" and reads the same
// on every pane in one directory. Never blank, so a fresh pane still has a row.
func railWindowLabel(customName, foregroundCmd, title string) string {
	if customName != "" {
		return customName
	}
	if foregroundCmd != "" {
		return foregroundCmd
	}
	if s := shellTitleLabel(title); s != "" {
		return s
	}
	return "shell"
}

// shellTitleLabel keeps the part of a shell's title that carries information:
// the last element of the directory it names. "~/dev/tuios - fish" says the
// same thing in every pane of one repo, where "tuios" at least says which repo,
// in a quarter of the columns.
func shellTitleLabel(title string) string {
	head, _, _ := strings.Cut(title, " - ")
	head = strings.TrimSpace(head)
	if head == "" {
		return strings.TrimSpace(title)
	}
	// bash writes "user@host:~/path"; the path is the half worth keeping.
	if _, after, ok := strings.Cut(head, ":"); ok && after != "" {
		head = after
	}
	if !strings.Contains(head, "/") {
		return head
	}
	if base := path.Base(head); base != "" && base != "/" && base != "." {
		return base
	}
	return head
}

// currentSessionInput builds the rich SessionInput for the session this client
// is attached to, entirely from live state with no network round trip. This is
// what the sidebar rebuilds every frame.
func (m *OS) currentSessionInput() sessiontree.SessionInput {
	windows := make([]sessiontree.WindowInput, 0, len(m.Windows))
	for i, w := range m.Windows {
		if w == nil {
			continue
		}
		windows = append(windows, sessiontree.WindowInput{
			ID:         w.ID,
			Title:      m.railTitleShown(w),
			AgentState: w.AgentState,
			DoneSeen:   m.agentSeen(w.ID),
			StateAt:    w.AgentStateAt,
			Harness:    w.AgentHarness,
			Focused:    i == m.FocusedWindow,
			Workspace:  w.Workspace,
		})
	}
	name := m.SessionName
	if name == "" {
		// Standalone mode has no daemon session name; present one synthetic
		// session so the surfaces still have a root to show.
		name = "local"
	}
	return sessiontree.SessionInput{
		Name:             name,
		DisplayName:      m.SessionDisplayName,
		Attached:         true,
		IsCurrent:        true,
		Restored:         m.SessionRestored,
		CurrentWorkspace: m.CurrentWorkspace,
		Windows:          windows,
	}
}

// foreignSessionInput builds the SessionInput for a session this client is not
// attached to, filling its windows from the client's cached listing so the
// sidebar can expand it. The cache is refreshed off the UI goroutine, so this
// stays a pure read with no round trip. Windows are nil until the first refresh
// lands, which leaves the row coarse (name only) exactly as before.
func (m *OS) foreignSessionInput(client *session.TUIClient, name string) sessiontree.SessionInput {
	summaries := client.SessionWindows(name)
	windows := make([]sessiontree.WindowInput, 0, len(summaries))
	for _, w := range summaries {
		windows = append(windows, sessiontree.WindowInput{
			ID: w.ID,
			// The daemon folds a custom name into Title and withholds a command
			// for a named pane, so passing no name here still lets one win.
			Title:      railWindowLabel("", w.ForegroundCmd, w.Title),
			AgentState: w.AgentState,
			DoneSeen:   m.agentSeen(w.ID),
			StateAt:    w.AgentStateAt,
			Harness:    w.AgentHarness,
			Workspace:  w.Workspace,
		})
	}
	display, _ := client.SessionLabel(name)
	return sessiontree.SessionInput{
		Name:             name,
		DisplayName:      display,
		Restored:         client.SessionRestored(name),
		CurrentWorkspace: client.SessionCurrentWorkspace(name),
		Windows:          windows,
	}
}

// BuildSessionTree builds the unified tree for the session-management surfaces.
// The attached session is built rich from live state; other sessions are added
// coarse (name only) from the client's CACHED session-name list.
//
// Ordering is a promise: sessions keep the daemon's creation order, with the
// attached session marked current IN PLACE rather than hoisted to the front.
// Hoisting looked helpful but meant every session switch reshuffled the list,
// so a row was never where the eye left it. The user's drag-defined order
// (SidebarOrder) overlays that base order; sessions it does not name keep
// their creation-order slots after the named ones, so a new session appends.
//
// It performs no daemon round trip. This is deliberate: the palette opens on the
// UI goroutine, and a blocking RefreshSessionList there froze the client and
// dropped the daemon connection while the daemon was busy (a browser flooding
// graphics over ssh). The cache is seeded on connect and refreshed off the UI
// goroutine (see the foreign-session refresh in Update), so non-attached
// sessions carry their window summaries and expand from the cache alone.
func (m *OS) BuildSessionTree() sessiontree.Tree {
	current := m.currentSessionInput()

	if m.DaemonClient == nil {
		return sessiontree.Build([]sessiontree.SessionInput{current})
	}

	names := m.DaemonClient.AvailableSessionNames()
	sessions := make([]sessiontree.SessionInput, 0, len(names)+1)
	seen := false
	for _, name := range names {
		if name == current.Name {
			sessions = append(sessions, current)
			seen = true
			continue
		}
		sessions = append(sessions, m.foreignSessionInput(m.DaemonClient, name))
	}
	if !seen {
		// The cache has not caught up with a just-created session yet; it goes
		// last, which is where the creation order will put it anyway.
		sessions = append(sessions, current)
	}
	sessions = orderByKey(sessions, func(s sessiontree.SessionInput) string { return s.Name }, m.SidebarOrder)
	return sessiontree.Build(sessions)
}

// railNeighbourSession returns the session delta places from the current one in
// the rail's own order, wrapping at both ends, or "" when there is nowhere to go.
// Reading the order from BuildSessionTree rather than the raw daemon listing is
// what makes "next" mean the row below the current one even after the user has
// dragged the rail into a different order.
func (m *OS) railNeighbourSession(delta int) string {
	sessions := m.BuildSessionTree().Sessions
	current := slices.IndexFunc(sessions, func(s sessiontree.Node) bool { return s.IsCurrent })
	if len(sessions) < 2 || current < 0 {
		return ""
	}
	n := len(sessions)
	return sessions[((current+delta)%n+n)%n].ID
}

// CycleSession switches to the next (delta 1) or previous (delta -1) session.
// Standalone has one synthetic session, so it says there is nowhere to go rather
// than failing inside SwitchToSession.
func (m *OS) CycleSession(delta int) {
	target := m.railNeighbourSession(delta)
	if target == "" {
		m.ShowNotification("No other sessions", "info", config.NotificationDuration)
		return
	}
	if err := m.SwitchToSession(target); err != nil {
		m.ShowNotification("Switch failed: "+err.Error(), "error", config.NotificationDuration*2)
	}
}

// sessionPaletteLabel formats a "Session: " or "Window: " palette row, folding
// in the agent-state glyph the same way the window title bar does, so the
// palette and the title bar never disagree about what a glyph means.
func sessionPaletteLabel(prefix, name, agentState string) string {
	if glyph := agentStateIndicator(agentState); glyph != "" {
		return prefix + glyph + " " + name
	}
	return prefix + name
}

// getSessionPaletteItems walks the unified session tree and returns one palette
// entry per session and one per window of every session, not only the attached
// one. This is what lets the palette jump straight to a session or a pane by
// name instead of going through the session switcher first; the sidebar, the
// switcher, and this list all read the same tree so they can never disagree
// about what exists or which one is current.
//
// A pane of a session this client is not attached to costs a session switch to
// reach, which is a heavier thing than the palette usually does: it tears down
// every PTY subscription and rebuilds the layout. So the row says so, in the
// slot a row's key hint would otherwise use, and the label carries the session
// it is in. The palette never performs it silently.
//
// Built once when the palette opens (see OpenCommandPalette), not on every
// render. BuildSessionTree is non-blocking (live state plus cached session
// names), so this is safe to call on the UI goroutine.
func getSessionPaletteItems(m *OS) []CommandPaletteItem {
	tree := m.BuildSessionTree()

	items := make([]CommandPaletteItem, 0, len(tree.Sessions))
	for _, s := range tree.Sessions {
		sessionName := s.ID
		isCurrent := s.IsCurrent
		items = append(items, CommandPaletteItem{
			Name:       sessionPaletteLabel("Session: ", sessionName, s.AgentState),
			Category:   "Sessions",
			AgentState: s.AgentState,
			Action: func(m *OS) (*OS, tea.Cmd) {
				if isCurrent {
					m.ShowNotification("Already on this session", "info", config.NotificationDuration)
					return m, nil
				}
				m.sidebarLeaveForJump()
				if err := m.SwitchToSession(sessionName); err != nil {
					m.ShowNotification("Switch failed: "+err.Error(), "error", config.NotificationDuration*2)
				}
				return m, nil
			},
		})

		for _, w := range s.Children {
			windowID, label, warn := w.ID, w.Title, ""
			if !isCurrent {
				// Qualified by session, the way the rail's own agent rows qualify a
				// pane that lives elsewhere, and marked with what selecting it costs.
				label, warn = sessionName+"/"+w.Title, "switches session"
			}
			items = append(items, CommandPaletteItem{
				Name:       sessionPaletteLabel("Window: ", label, w.AgentState),
				Shortcut:   warn,
				Category:   "Sessions",
				AgentState: w.AgentState,
				Action: func(m *OS) (*OS, tea.Cmd) {
					m.sidebarLeaveForJump()
					if !isCurrent {
						if err := m.SwitchToSession(sessionName); err != nil {
							m.ShowNotification("Switch failed: "+err.Error(), "error", config.NotificationDuration*2)
							return m, nil
						}
					}
					m.focusWindowByID(windowID)
					return m, nil
				},
			})
		}
	}
	return items
}

// focusWindowByID focuses the pane with the given ID among the windows this
// client holds, and says nothing when it holds none: after a session switch the
// daemon's listing can name a pane the restored state no longer has.
func (m *OS) focusWindowByID(id string) {
	if i := m.windowIndexByID(id); i >= 0 {
		m.FocusWindow(i)
	}
}

// sidebarLeaveForJump hands the keyboard back to the panes when a palette row
// opened from the rail is about to relocate the user. Attaching to a session and
// focusing a pane are the two things the rail already treats as "this is where I
// asked to end up" rather than as a browse, so the return record is dropped
// first: restoring the pane the rail borrowed from would undo the jump.
func (m *OS) sidebarLeaveForJump() {
	if !m.SidebarFocused {
		return
	}
	m.clearSidebarReturn()
	m.ExitSidebarFocus()
}
