package app

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// railTitleDebounce is how long a window title must hold before the sidebar
// adopts a change. Bursty commands (a shell writing the running command to the
// title) churn the title several times a second; without this the rail row
// text thrashes. An isolated change still shows within a tick, because the
// debounce is measured from the last adopted title, not from the last change.
const railTitleDebounce = 200 * time.Millisecond

// railTitleEntry is the title the sidebar currently shows for a window and when
// it adopted it.
type railTitleEntry struct {
	shown string
	at    time.Time
}

// railTitleShown returns the title the sidebar should display for a live
// window: the debounced value once seeded, else the live title so a freshly
// spawned pane is never blank. Pure read; the tick owns the bookkeeping.
func (m *OS) railTitleShown(w *terminal.Window) string {
	if e, ok := m.sidebarTitles[w.ID]; ok {
		return e.shown
	}
	return windowRowTitle(w)
}

// updateRailTitles advances the debounced sidebar titles one tick. A title that
// has drifted from the shown one is adopted once railTitleDebounce has passed
// since the last adoption, coalescing a burst into at most one rail update per
// interval while still settling on the final title. It returns whether an
// adoption changed a displayed title (the sidebar must redraw) and records
// whether any change is still pending (the tick must keep running to settle it).
func (m *OS) updateRailTitles() (changed bool) {
	if m.sidebarTitles == nil {
		m.sidebarTitles = make(map[string]railTitleEntry, len(m.Windows))
	}
	now := time.Now()
	pending := false
	for _, w := range m.Windows {
		if w == nil {
			continue
		}
		live := windowRowTitle(w)
		e, ok := m.sidebarTitles[w.ID]
		switch {
		case !ok:
			m.sidebarTitles[w.ID] = railTitleEntry{shown: live, at: now}
		case live == e.shown:
			// In sync; nothing to do.
		case now.Sub(e.at) >= railTitleDebounce:
			m.sidebarTitles[w.ID] = railTitleEntry{shown: live, at: now}
			changed = true
		default:
			pending = true
		}
	}
	// Drop entries for closed windows so the map cannot outgrow the window set.
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
