package app

import (
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
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
	// and the bare trailing columns). All absolute.
	X0, X1, DismissX0, Y int
	// Drawn is the message the block showed, as it was on that frame. A click
	// acts on this message only if it is still the one on top and still says
	// the same thing: a clipboard ask whose text changed after the frame must
	// not be allowed by a click aimed at the old text.
	Drawn Notification
}

// notifTargetedIndex is the newest message carrying a target, or -1. Activation
// walks down from the top of the queue, so repeat presses of the keyboard twin
// go deeper as each one is dismissed.
func (m *OS) notifTargetedIndex() int {
	for i := len(m.Notifications) - 1; i >= 0; i-- {
		// A clipboard ask is allowed only by a click on it. The jump key is
		// pressed from habit, and one stray press must not hand a background
		// pane the clipboard.
		if t := m.Notifications[i].Target; t != nil && t.ClipboardAsk == 0 {
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

// jumpToNotifTarget lands on a message's source pane and reports whether the
// target was served: jumped, answered, or already reported as gone. It reuses
// the rail's own
// focus routine, so a jump from a message and a click on the rail cannot
// disagree about what "go there" means: FocusWindow already switches workspace,
// and sidebarFocusWindow already switches session first.
func (m *OS) jumpToNotifTarget(t NotifTarget) bool {
	return m.jumpTarget(t, true)
}

// jumpTarget is the jump itself. record says whether the jump owes the back
// stack an entry: a jump made BY jump_back must not, or every press would
// grow the stack and back and forth would never drain it.
func (m *OS) jumpTarget(t NotifTarget, record bool) bool {
	// A message that asks to let a pane set the clipboard is answered, not
	// followed.
	if t.ClipboardAsk != 0 {
		m.allowClipboardAsk(t.ClipboardAsk, t.ClipboardVersion)
		return true
	}
	// A message about mail lands on the thread, where the reply is, rather than
	// on the pane that wrote. The marking read it may need is queued, since
	// this runs inside handlers that return no command.
	if t.Thread != 0 {
		if m.OpenAgentMailThread(t.Thread) != nil {
			m.QueueClientEvent(ClientEvent{Type: "agent-mail-mark", Mail: session.AgentMailPayload{ReadIDs: []uint64{t.Thread}}})
		}
		return true
	}
	// A message about another machine attaches that machine first, landing on
	// the session, the way the Inbox does.
	if t.Host != "" && t.Host != m.attachedMachine() {
		it := session.AttentionItem{Session: t.SessionID, Window: t.WindowID}
		if t.Host != federation.LocalHostName {
			it.Host = t.Host
		}
		if !m.inboxReach(it) {
			return false
		}
	}
	foreign := t.SessionID != "" && t.SessionID != m.sidebarCurrentSessionID()
	if foreign && !m.sessionCached(t.SessionID) {
		m.ShowNotification("Source session closed", "info", m.Settings.NotificationDuration)
		return false
	}

	idx := -1
	if !foreign {
		idx = m.windowIndexByID(t.WindowID)
		if idx < 0 {
			// The sidebar, the CLI and $TUIOS_WINDOW_ID all speak in the short
			// form of the id, so a link a pane printed carries the short form
			// too. Resolve it against the panes that exist; an ambiguous or
			// empty prefix is as good as a dead one.
			full, matched := m.expandWindowID(t.WindowID)
			if !matched {
				m.ShowNotification("Source pane closed", "info", m.Settings.NotificationDuration)
				return false
			}
			t.WindowID = full
			idx = m.windowIndexByID(full)
		}
	}
	if record {
		m.recordJumpOrigin()
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
		m.ShowNotification("Source pane closed", "info", m.Settings.NotificationDuration)
		return false
	}
	// Flash the pane the jump landed on, using the dock's existing time-bounded
	// highlight, so the eye follows a long-distance focus change.
	m.Windows[landed].MinimizeHighlightUntil = time.Now().Add(time.Second)
	return true
}

// linkTargetAlive reports whether the pane a tuios:// link names is still
// somewhere the jump could land. A pane's text outlives the panes it points
// at, so a link whose target has closed stops being one: no hover, no click.
// Anything that is not our scheme has no target to die.
func (m *OS) linkTargetAlive(link PaneLink) bool {
	target, ok := tuiosLinkTarget(link.URL)
	if !ok {
		return true
	}
	if target.SessionID != "" && target.SessionID != m.sidebarCurrentSessionID() {
		return m.sessionCached(target.SessionID)
	}
	for _, w := range m.Windows {
		if w != nil && strings.HasPrefix(w.ID, target.WindowID) {
			return true
		}
	}
	return false
}

// expandWindowID resolves a window id that may be a prefix of the full form.
// An exact match wins; otherwise a prefix naming exactly one live pane expands
// to it. Zero or several matches resolve to false.
func (m *OS) expandWindowID(id string) (string, bool) {
	if id == "" {
		return "", false
	}
	matches := make([]string, 0, 1)
	for _, w := range m.Windows {
		if w != nil && strings.HasPrefix(w.ID, id) {
			matches = append(matches, w.ID)
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return "", false
}

// sessionCached reports whether a session is still one the client could attach
// to. It reads the cached listing rather than tape_run's sessionExists, which
// does a daemon round trip: this runs on the UI goroutine, where blocking on a
// busy daemon is what froze the client (see BuildSessionTree).
func (m *OS) sessionCached(name string) bool {
	if m.DaemonClient == nil {
		return false
	}
	return slices.Contains(m.DaemonClient.AvailableSessionNames(), name)
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
	m.clickVisibleNotification()
	return true
}

// clickVisibleNotification activates the message on top of the dock, if it is
// the message the last frame drew. A message that changed since then is left
// as it is, and the next frame shows the new text.
func (m *OS) clickVisibleNotification() {
	n := len(m.Notifications)
	if n == 0 {
		return
	}
	visible := m.Notifications[n-1]
	if !sameDrawnNotification(visible, m.notifHit.Drawn) {
		return
	}
	m.Notifications = m.Notifications[:n-1]
	if visible.Target != nil {
		m.jumpToNotifTarget(*visible.Target)
	}
}

// sameDrawnNotification reports whether a is the message b was drawn from,
// with the same text and the same target.
func sameDrawnNotification(a, b Notification) bool {
	if a.ID != b.ID || a.Message != b.Message || (a.Target == nil) != (b.Target == nil) {
		return false
	}
	return a.Target == nil || *a.Target == *b.Target
}

// drawnCopy is n with its target copied, so a later change to the live
// message's target does not change the record of what was drawn.
func (m *OS) drawnCopy(n Notification) Notification {
	if n.Target != nil {
		t := *n.Target
		n.Target = &t
	}
	return n
}

// jumpBackDepth bounds the stack. A jump a minute for an hour is a stack of
// sixty nobody would walk; past this, the oldest origin falls off.
const jumpBackDepth = 32

// recordJumpOrigin notes the pane the user is looking at now, for JumpBack to
// restore. Called from jumpTarget just before it moves, so a jump that fails
// its dead-target checks records nothing.
func (m *OS) recordJumpOrigin() {
	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}
	w := m.Windows[m.FocusedWindow]
	m.jumpBackStack = append(m.jumpBackStack, NotifTarget{
		SessionID: m.sidebarCurrentSessionID(),
		WindowID:  w.ID,
	})
	if len(m.jumpBackStack) > jumpBackDepth {
		m.jumpBackStack = m.jumpBackStack[1:]
	}
}

// JumpBack undoes the newest jump: focus goes to where the user was looking
// when it happened. Back jumps record nothing, so walking the stack drains it
// and a press that lands home reports there is nothing left. Entries whose
// pane has closed are dropped on the way, and the walk continues past them.
func (m *OS) JumpBack() bool {
	for len(m.jumpBackStack) > 0 {
		t := m.jumpBackStack[len(m.jumpBackStack)-1]
		m.jumpBackStack = m.jumpBackStack[:len(m.jumpBackStack)-1]
		if m.jumpTarget(t, false) {
			return true
		}
	}
	return false
}
