package app

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A message about a pane is a pointer to that pane, so it may as well be
// followed. The block's body activates (jump there, then dismiss, because going
// there IS the acknowledgment) and its right-hand end dismisses without
// jumping; prefix+j is the keyboard's version of clicking the body.
//
// Dead targets are the normal case, not an edge case: a message outlives the
// pane that raised it all the time. Every one of them degrades to a short
// info message, never an error and never a jump to whatever now happens to
// hold that index.

// notifHitZones is where the message block sat on the last drawn frame. Recorded
// by the renderer because only it knows the block's real width.
type notifHitZones struct {
	Active bool
	// X0 is the block's first column and X1 the column past its last, so the
	// pair is the block's extent and the burn rule above it is drawn to the same
	// span. DismissX0 opens the right-hand zone (the counter, the esc affordance
	// and the closing cap). All absolute.
	X0, X1, DismissX0, Y int
}

// notifTargetedIndex is the newest message carrying a target, or -1. Activation
// walks down from the top of the queue, so repeat presses of the keyboard twin
// go deeper as each one is dismissed.
func (m *OS) notifTargetedIndex() int {
	for i := len(m.Notifications) - 1; i >= 0; i-- {
		if m.Notifications[i].Target != nil {
			return i
		}
	}
	return -1
}

// JumpToNotification activates the newest targeted message: jump to its pane,
// then take it off the dock. It reports whether anything was activated, so the
// keybinding can fall through when there is nothing to jump to.
func (m *OS) JumpToNotification() bool {
	i := m.notifTargetedIndex()
	if i < 0 {
		return false
	}
	target := *m.Notifications[i].Target
	m.Notifications = append(m.Notifications[:i], m.Notifications[i+1:]...)
	m.jumpToNotifTarget(target)
	return true
}

// jumpToNotifTarget lands on a message's source pane. It reuses the rail's own
// focus routine, so a jump from a message and a click on the rail cannot
// disagree about what "go there" means: FocusWindow already switches workspace,
// and sidebarFocusWindow already switches session first.
func (m *OS) jumpToNotifTarget(t NotifTarget) {
	foreign := t.SessionID != "" && t.SessionID != m.sidebarCurrentSessionID()
	if foreign && !m.sessionCached(t.SessionID) {
		m.ShowNotification("Source session closed", "info", config.NotificationDuration)
		return
	}

	idx := -1
	if !foreign {
		idx = m.windowIndexByID(t.WindowID)
		if idx < 0 {
			m.ShowNotification("Source pane closed", "info", config.NotificationDuration)
			return
		}
	}
	m.sidebarFocusWindow(sidebarRowHit{
		Kind:        sidebarRowWindow,
		SessionID:   t.SessionID,
		WindowID:    t.WindowID,
		WindowIndex: idx,
	})

	landed := m.windowIndexByID(t.WindowID)
	if landed < 0 {
		// The switch went through but the pane is gone on the other side.
		m.ShowNotification("Source pane closed", "info", config.NotificationDuration)
		return
	}
	// Flash the pane the jump landed on, using the dock's existing time-bounded
	// highlight, so the eye follows a long-distance focus change.
	m.Windows[landed].MinimizeHighlightUntil = time.Now().Add(time.Second)
}

// sessionCached reports whether a session is still one the client could attach
// to. It reads the cached listing rather than tape_run's sessionExists, which
// does a daemon round trip: this runs on the UI goroutine, where blocking on a
// busy daemon is what froze the client (see BuildSessionTree).
func (m *OS) sessionCached(name string) bool {
	if m.DaemonClient == nil {
		return false
	}
	for _, n := range m.DaemonClient.AvailableSessionNames() {
		if n == name {
			return true
		}
	}
	return false
}

// NotificationClick routes a press inside the message block: its right-hand end
// dismisses the visible message, the rest of it activates. It reports whether
// the press was inside the block at all.
func (m *OS) NotificationClick(x, y int) bool {
	z := m.notifHit
	if !z.Active || len(m.Notifications) == 0 || y != z.Y || x < z.X0 || x >= z.X1 {
		return false
	}
	if x >= z.DismissX0 {
		m.dismissVisibleNotification()
		return true
	}
	if n := len(m.Notifications); n > 0 {
		visible := m.Notifications[n-1]
		m.Notifications = m.Notifications[:n-1]
		if visible.Target != nil {
			m.jumpToNotifTarget(*visible.Target)
		}
	}
	return true
}
