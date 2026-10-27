package app

import (
	"encoding/json"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TML surfaces in Learn mode. The browser tab has no daemon to hold a
// surface, so the tour's host (internal/learn) holds it instead: the tree,
// the state store and the versions, as session/tml_surface.go does. The
// client half is the one every session runs (tml.go): it draws the surface
// with loom, takes the keys and the mouse, and sends tml-input, tml-get and
// tml-place to the host in place of the daemon. Nothing here runs outside
// Learn mode.

// LearnTMLPlaceholder is the program a TML window runs in Learn mode. The
// fake shell holds the pane with it, as the daemon's placeholder does.
const LearnTMLPlaceholder = "tuios-tml"

// SetLearnTMLHost installs Learn mode's stand-in for the daemon's TML verbs.
// It does nothing outside Learn mode. Call it before the program runs.
func (m *OS) SetLearnTMLHost(call func(verb string, params map[string]any, timeout time.Duration) (json.RawMessage, error)) {
	if !m.LearnMode {
		return
	}
	m.tml.local = call
}

// LearnTMLWindow says how Learn mode opens the window of a surface.
type LearnTMLWindow struct {
	Surface string
	// Name is the window's name, "from OWNER · TITLE", as the daemon
	// writes it (session.tmlWindowName).
	Name string
	// Popup floats the surface over the panes. Otherwise it is a tiled
	// pane.
	Popup         bool
	Width, Height string
	// Focus gives the surface the keys. The daemon gives them only to a
	// surface the person opened from outside every pane.
	Focus bool
}

// OpenLearnTMLWindow opens the window a surface is drawn in, in Learn mode,
// and returns its id, or "" when it did not open. It runs on the UI
// goroutine. The surface itself arrives with LearnPushTMLSurface.
func (m *OS) OpenLearnTMLWindow(o LearnTMLWindow) string {
	if !m.LearnMode || m.tml.local == nil {
		return ""
	}
	prev := m.GetFocusedWindowID()
	var w *terminal.Window
	if o.Popup {
		w = m.newLocalPopup("", o.Name, o.Width, o.Height, []string{LearnTMLPlaceholder})
	} else {
		before := len(m.Windows)
		m.AddWindow(o.Name, LearnTMLPlaceholder)
		if len(m.Windows) > before {
			w = m.Windows[len(m.Windows)-1]
		}
	}
	if w == nil {
		return ""
	}
	w.Kind = session.WindowKindTML
	w.URI = session.TMLURIPrefix + o.Surface
	if o.Focus {
		m.FocusWindow(len(m.Windows) - 1)
	} else if prev != "" {
		// A new pane takes the focus as it opens. The keys go back where
		// they were.
		for i, win := range m.Windows {
			if win.ID == prev {
				m.FocusWindow(i)
				break
			}
		}
	}
	m.MarkAllDirty()
	return w.ID
}

// LearnPushTMLSurface hands a surface from Learn mode's host to the client
// half, as the daemon's push does. It is safe from any goroutine.
func (m *OS) LearnPushTMLSurface(p *session.TMLSurfacePayload) {
	if m.tml.local == nil || p == nil {
		return
	}
	m.queueTMLSurface(p)
}
