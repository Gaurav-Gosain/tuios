package app

import (
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
// coarse (name, window count, attached flag) from the daemon's session list.
//
// It performs one daemon round trip (RefreshSessionList) when attached, so it
// belongs on a surface-open or a throttled refresh, not in the per-frame render
// path; the sidebar's per-frame build uses currentSessionInput directly.
func (m *OS) BuildSessionTree() sessiontree.Tree {
	current := m.currentSessionInput()

	if m.DaemonClient == nil {
		return sessiontree.Build([]sessiontree.SessionInput{current})
	}

	infos, err := m.DaemonClient.RefreshSessionList()
	if err != nil {
		return sessiontree.Build([]sessiontree.SessionInput{current})
	}

	sessions := make([]sessiontree.SessionInput, 0, len(infos)+1)
	sawCurrent := false
	for _, info := range infos {
		if info.Name == m.SessionName && m.SessionName != "" {
			// Keep the rich current session but trust the daemon's attached flag.
			current.Attached = info.Attached
			sessions = append(sessions, current)
			sawCurrent = true
			continue
		}
		sessions = append(sessions, sessiontree.SessionInput{
			Name:        info.Name,
			Attached:    info.Attached,
			WindowCount: info.WindowCount,
		})
	}
	if !sawCurrent {
		// The attached session was not in the list (transient); show it anyway.
		sessions = append([]sessiontree.SessionInput{current}, sessions...)
	}
	return sessiontree.Build(sessions)
}
