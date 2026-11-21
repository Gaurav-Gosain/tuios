package app

import (
	"slices"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

type notifHitZones struct {
	Active               bool
	X0, X1, DismissX0, Y int
	Drawn                Notification
	Cut                  bool
	Choices              []notifSpan
}

func (m *OS) notifTargetedIndex() int {
	for i, v := range slices.Backward(m.Notifications) {
		if t := v.Target; t != nil && t.ClipboardAsk == 0 {
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

func (m *OS) jumpToNotifTarget(t NotifTarget) {
	if t.ClipboardAsk != 0 {
		m.allowClipboardAsk(t.ClipboardAsk, t.ClipboardVersion)
		return
	}
	if t.DaemonRestart {
		m.OpenDaemonRestart()
		return
	}
	if t.OfferTarget != "" {
		m.answerMachineOffer(t, offerAdd)
		return
	}
	if t.AgentInstall != "" {
		m.installFromNotice(t.AgentInstall)
		return
	}
	if t.FarOpen != 0 {
		m.acceptFarOpen(t.FarOpen)
		return
	}
	if t.Thread != 0 {
		if m.OpenAgentMailThread(t.Thread) != nil {
			m.QueueClientEvent(ClientEvent{Type: "agent-mail-mark", Mail: session.AgentMailPayload{ReadIDs: []uint64{t.Thread}}})
		}
		return
	}
	if t.Host != "" && t.Host != m.attachedMachine() {
		it := session.AttentionItem{Session: t.SessionID, Window: t.WindowID}
		if t.Host != federation.LocalHostName {
			it.Host = t.Host
		}
		if !m.inboxReach(it) {
			return
		}
	}
	foreign := t.SessionID != "" && t.SessionID != m.sidebarCurrentSessionID()
	if foreign && !m.sessionCached(t.SessionID) {
		m.ShowNotification("Source session closed", "info", m.Settings.NotificationDuration)
		return
	}

	idx := -1
	if !foreign {
		idx = m.windowIndexByID(t.WindowID)
		if idx < 0 {
			m.ShowNotification("Source pane closed", "info", m.Settings.NotificationDuration)
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
		m.ShowNotification("Source pane closed", "info", m.Settings.NotificationDuration)
		return
	}
	m.Windows[landed].MinimizeHighlightUntil = time.Now().Add(time.Second)
}

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
	if top := m.Notifications[len(m.Notifications)-1]; isMachineOffer(top.Target) && sameDrawnNotification(top, z.Drawn) {
		for i, c := range z.Choices {
			if x >= c.X0 && x < c.X1 {
				m.answerMachineOffer(*top.Target, i)
				return true
			}
		}
		if x >= z.DismissX0 {
			m.answerMachineOffer(*top.Target, offerNotNow)
		}
		return true
	}
	if x >= z.DismissX0 {
		if top := m.Notifications[len(m.Notifications)-1]; sameDrawnNotification(top, z.Drawn) {
			m.noteNoticesDismissed([]Notification{top})
		}
		m.dismissVisibleNotification()
		return true
	}
	m.clickVisibleNotification()
	return true
}

func (m *OS) clickVisibleNotification() {
	n := len(m.Notifications)
	if n == 0 {
		return
	}
	visible := m.Notifications[n-1]
	if !sameDrawnNotification(visible, m.notifHit.Drawn) {
		return
	}
	if t := visible.Target; isClipboardAsk(t) && m.clipboardAskSettling(t.ClipboardAsk) {
		return
	}
	m.Notifications = m.Notifications[:n-1]
	if m.notifHit.Cut && !isClipboardAsk(visible.Target) && !isRestartNotice(visible.Target) && !isInstallNotice(visible.Target) && !isFarOpenAsk(visible.Target) {
		m.notifHoverEnd()
		m.openMessageView(m.notificationEntry(visible))
		return
	}
	if visible.Target != nil {
		m.jumpToNotifTarget(*visible.Target)
	}
}

func isClipboardAsk(t *NotifTarget) bool { return t != nil && t.ClipboardAsk != 0 }

func isRestartNotice(t *NotifTarget) bool { return t != nil && t.DaemonRestart }

func isInstallNotice(t *NotifTarget) bool { return t != nil && t.AgentInstall != "" }

func sameDrawnNotification(a, b Notification) bool {
	if a.ID != b.ID || a.Message != b.Message || (a.Target == nil) != (b.Target == nil) {
		return false
	}
	return a.Target == nil || *a.Target == *b.Target
}

func (m *OS) drawnCopy(n Notification) Notification {
	if n.Target != nil {
		t := *n.Target
		n.Target = &t
	}
	return n
}
