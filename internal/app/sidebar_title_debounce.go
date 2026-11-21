package app

import (
	"slices"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

const railTitleDebounce = 200 * time.Millisecond

type railTitleEntry struct {
	shown string
	at    time.Time
}

func (m *OS) railTitleShown(w *terminal.Window) string {
	if e, ok := m.sidebarTitles[w.ID]; ok {
		return e.shown
	}
	return windowRowTitle(w)
}

func (m *OS) updateRailTitles() (changed bool) {
	if m.sidebarTitles == nil {
		m.sidebarTitles = make(map[string]railTitleEntry, len(m.Windows))
	}
	daemonTitles := m.unwatchedTitles()
	if m.DaemonClient != nil {
		m.sidebarTitleGen = m.DaemonClient.CacheGen()
	}
	now := time.Now()
	pending := false
	for _, w := range m.Windows {
		if w == nil {
			continue
		}
		live := windowRowTitle(w)
		if t, ok := daemonTitles[w.ID]; ok {
			live = t
		}
		e, ok := m.sidebarTitles[w.ID]
		switch {
		case !ok:
			m.sidebarTitles[w.ID] = railTitleEntry{shown: live, at: now}
		case live == e.shown:
		case now.Sub(e.at) >= railTitleDebounce:
			m.sidebarTitles[w.ID] = railTitleEntry{shown: live, at: now}
			changed = true
		default:
			pending = true
		}
	}
	if len(m.sidebarTitles) > len(m.Windows) {
		for id := range m.sidebarTitles {
			if m.windowIndexByID(id) < 0 {
				delete(m.sidebarTitles, id)
			}
		}
	}
	m.sidebarTitlePending = pending
	return changed
}

func (m *OS) unwatchedTitles() map[string]string {
	if m.DaemonClient == nil {
		return nil
	}
	unwatched := func(w *terminal.Window) bool {
		return w != nil && w.DaemonMode && w.CustomName == "" &&
			w.PTYID != "" && !m.SubscribedPTYs[w.PTYID]
	}
	any := slices.ContainsFunc(m.Windows, unwatched)
	if !any {
		return nil
	}

	summaries := m.DaemonClient.SessionWindows(m.SessionName)
	if len(summaries) == 0 {
		return nil
	}
	byID := make(map[string]session.WindowSummary, len(summaries))
	for _, s := range summaries {
		byID[s.ID] = s
	}
	titles := make(map[string]string, len(summaries))
	for _, w := range m.Windows {
		if !unwatched(w) {
			continue
		}
		if s, ok := byID[w.ID]; ok && s.Title != "" {
			titles[w.ID] = railWindowLabel("", s.AgentHarness, s.ForegroundCmd, s.Title)
		}
	}
	return titles
}
