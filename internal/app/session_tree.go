package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// windowRowTitle is the label a session-management surface shows for a window:
// the user's custom name if set, else the live terminal title, else a short
// fallback so a freshly spawned pane is never blank.
func windowRowTitle(w *terminal.Window) string {
	if w.CustomName != "" {
		return w.CustomName
	}
	if t := w.Title(); t != "" {
		return t
	}
	return "shell"
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
			Title:      windowRowTitle(w),
			AgentState: w.AgentState,
			Focused:    i == m.FocusedWindow,
		})
	}
	name := m.SessionName
	if name == "" {
		// Standalone mode has no daemon session name; present one synthetic
		// session so the surfaces still have a root to show.
		name = "local"
	}
	return sessiontree.SessionInput{
		Name:      name,
		Attached:  true,
		IsCurrent: true,
		Windows:   windows,
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
// graphics over ssh). The cache is seeded on connect and refreshed whenever the
// session switcher is opened, so it is fresh enough to list sessions by name;
// per-window detail for a non-attached session fills in once it is switched to.
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
		sessions = append(sessions, sessiontree.SessionInput{Name: name})
	}
	if !seen {
		// The cache has not caught up with a just-created session yet; it goes
		// last, which is where the creation order will put it anyway.
		sessions = append(sessions, current)
	}
	sessions = orderByKey(sessions, func(s sessiontree.SessionInput) string { return s.Name }, m.SidebarOrder)
	return sessiontree.Build(sessions)
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

// getSessionPaletteItems walks the unified session tree and returns one
// palette entry per session plus, for the attached session, one entry per its
// windows. This is what lets the palette jump straight to a session or a
// window by name instead of going through the session switcher first; the
// sidebar, the switcher, and this list all read the same tree so they can
// never disagree about what exists or which one is current.
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
				if err := m.SwitchToSession(sessionName); err != nil {
					m.ShowNotification("Switch failed: "+err.Error(), "error", config.NotificationDuration*2)
				}
				return m, nil
			},
		})

		if !s.IsCurrent {
			continue
		}
		for _, w := range s.Children {
			windowID := w.ID
			items = append(items, CommandPaletteItem{
				Name:       sessionPaletteLabel("Window: ", w.Title, w.AgentState),
				Category:   "Sessions",
				AgentState: w.AgentState,
				Action: func(m *OS) (*OS, tea.Cmd) {
					for i, win := range m.Windows {
						if win != nil && win.ID == windowID {
							m.FocusWindow(i)
							break
						}
					}
					return m, nil
				},
			})
		}
	}
	return items
}
