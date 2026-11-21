package app

import (
	"log"
	"sync"

	tea "charm.land/bubbletea/v2"
	"tuios.dev/swatch"

	"github.com/Gaurav-Gosain/tuios/internal/crashexit"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

type omarchyFollowMsg struct {
	palette swatch.Palette
}

type omarchyFollowHub struct {
	mu      sync.Mutex
	watcher *swatch.Watcher
	subs    map[chan swatch.Palette]struct{}
}

var omarchyFollow omarchyFollowHub

var omarchyWatchSource = func() swatch.Source { return theme.OmarchySource() }

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

func (h *omarchyFollowHub) forward(w *swatch.Watcher) {
	defer crashexit.Recover()
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

func (m *OS) configuredTheme() string {
	if m.UserConfig == nil {
		return ""
	}
	return m.UserConfig.Appearance.Theme
}

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

func (m *OS) endOmarchyFollow() {
	if m.stopOmarchyFollow != nil {
		m.stopOmarchyFollow()
		m.stopOmarchyFollow = nil
	}
	m.omarchyChanges = nil
}

func (m *OS) applyOmarchyPalette(p swatch.Palette) {
	theme.SetOmarchyPalette(p)
	if theme.CurrentThemeID() != theme.OmarchyID {
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
