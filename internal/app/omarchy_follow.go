package app

import (
	"log"
	"sync"

	tea "charm.land/bubbletea/v2"
	"tuios.dev/swatch"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Following the Omarchy theme while tuios runs.
//
// Omarchy switches its theme by replacing one directory (see
// tuios.dev/swatch/omarchy). A swatch.Watcher sees the switch and reads the
// new palette, and every session of this process gets it as an
// omarchyFollowMsg. Update registers the palette as the omarchy theme and,
// when that theme is the one in force, recolours the panes and the chrome and
// tells the panes and the daemon the new colours. A pane whose program turned
// on mode 2031 gets a light or dark report from its emulator when the
// background changes kind (see vt/color_scheme.go). No pane gets a
// SIGWINCH.
//
// One watcher per process, as for the config file: the first session to
// subscribe starts it and the last to leave stops it. It is started only on a
// machine with Omarchy, or when the config asks for the omarchy theme, so a
// machine without Omarchy watches nothing. The watcher holds one inotify
// watch and one goroutine blocked on it, and runs no timer while the theme
// stays the same.

// omarchyFollowMsg is a new Omarchy palette.
type omarchyFollowMsg struct {
	palette swatch.Palette
}

// omarchyFollowHub fans one swatch.Watcher out to the sessions of this
// process.
type omarchyFollowHub struct {
	mu      sync.Mutex
	watcher *swatch.Watcher
	subs    map[chan swatch.Palette]struct{}
}

var omarchyFollow omarchyFollowHub

// omarchyWatchSource is the source the hub follows. A variable for tests.
var omarchyWatchSource = func() swatch.Source { return theme.OmarchySource() }

// subscribe returns a channel the hub delivers palettes on, holding one, and
// the function that ends the subscription.
func (h *omarchyFollowHub) subscribe() (<-chan swatch.Palette, func()) {
	ch := make(chan swatch.Palette, 1)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs == nil {
		h.subs = make(map[chan swatch.Palette]struct{})
	}
	h.subs[ch] = struct{}{}
	if h.watcher == nil {
		w, err := swatch.Watch(omarchyWatchSource())
		if err != nil {
			log.Printf("Omarchy theme watcher unavailable, a theme switch needs a restart: %v", err)
		} else {
			h.watcher = w
			go h.forward(w)
		}
	}
	return ch, func() { h.unsubscribe(ch) }
}

// forward hands each palette the watcher reads to every session, newest
// wins. It ends when the watcher is closed.
func (h *omarchyFollowHub) forward(w *swatch.Watcher) {
	for p := range w.Changes() {
		h.mu.Lock()
		for ch := range h.subs {
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- p:
			default:
			}
		}
		h.mu.Unlock()
	}
}

// unsubscribe drops one session. The last one out closes the watcher,
// outside the lock, because forward takes the lock to deliver.
func (h *omarchyFollowHub) unsubscribe(ch chan swatch.Palette) {
	h.mu.Lock()
	delete(h.subs, ch)
	close(ch)
	var w *swatch.Watcher
	if len(h.subs) == 0 {
		w, h.watcher = h.watcher, nil
	}
	h.mu.Unlock()
	if w != nil {
		_ = w.Close()
	}
}

// configuredTheme is the theme name the config file sets.
func (m *OS) configuredTheme() string {
	if m.UserConfig == nil {
		return ""
	}
	return m.UserConfig.Appearance.Theme
}

// startOmarchyFollow subscribes this session to the Omarchy theme, on a
// machine with Omarchy or when the config asks for it, and returns the
// command that waits for the first change.
func (m *OS) startOmarchyFollow() tea.Cmd {
	if !m.watchesConfig() || m.omarchyChanges != nil {
		return nil
	}
	if m.configuredTheme() != theme.OmarchyID && !theme.OmarchyInstalled() {
		return nil
	}
	m.omarchyChanges, m.stopOmarchyFollow = omarchyFollow.subscribe()
	return listenForOmarchy(m.omarchyChanges)
}

// listenForOmarchy waits for one palette from the hub.
func listenForOmarchy(ch <-chan swatch.Palette) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		p, ok := <-ch
		if !ok {
			return nil
		}
		return omarchyFollowMsg{palette: p}
	}
}

// endOmarchyFollow ends this session's subscription.
func (m *OS) endOmarchyFollow() {
	if m.stopOmarchyFollow != nil {
		m.stopOmarchyFollow()
		m.stopOmarchyFollow = nil
	}
	m.omarchyChanges = nil
}

// applyOmarchyPalette puts a new Omarchy palette into force. Every session
// of the process runs it for the same palette: the first registers it, and
// each repaints its own panes.
func (m *OS) applyOmarchyPalette(p swatch.Palette) {
	theme.SetOmarchyPalette(p)
	if theme.CurrentThemeID() != theme.OmarchyID {
		// Omarchy appeared after tuios started, and the config follows it.
		// A theme picked by hand, or a preview in the picker, stays.
		if theme.CurrentThemeID() != "" || !theme.FollowsOmarchy(m.configuredTheme()) {
			return
		}
		if err := theme.Initialize(theme.OmarchyID); err != nil {
			return
		}
	}
	m.UpdateAllWindowThemes()
	m.MarkAllDirty()
	m.syncReportColors()
	m.SyncStateToDaemon()
}
