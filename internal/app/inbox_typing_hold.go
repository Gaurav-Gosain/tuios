package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

const inboxTypingWords = "Paused while you type"

type inboxArrival struct {
	key string
	at  time.Time
}

type inboxTypingReleaseMsg struct{ seq uint64 }

func inboxTypingKind(it session.AttentionItem) bool {
	return inboxHeld(it) || (it.Kind == session.AttentionAsk && it.RequestID != "")
}

func (m *OS) inboxReplyFocused() bool {
	return m.InboxReplyOpen() && !m.CockpitImageOpen() && !m.CockpitSpawnOpen() &&
		!m.CockpitCompareOpen() && !m.CockpitArtifactFocused() && !m.InboxPeeking() &&
		!m.cockpitReplyMismatch()
}

func (m *OS) noteInboxTyped() {
	if ed := m.Inbox.reply.editor; ed != nil && !m.ProcessingRemoteKeys {
		ed.typing.Key(time.Now())
	}
}

func (m *OS) noteInboxArrival(prev *session.AttentionItem, it session.AttentionItem, now time.Time) {
	ed := m.Inbox.reply.editor
	if ed == nil || !inboxTypingKind(it) {
		return
	}
	key := inboxShownKey(it)
	if prev != nil && inboxShownKey(*prev) == key {
		return
	}
	if a, ok := ed.arrived[it.ID]; ok && a.key == key {
		return
	}
	if ed.arrived == nil {
		ed.arrived = make(map[string]inboxArrival)
	}
	ed.arrived[it.ID] = inboxArrival{key: key, at: now}
}

func (m *OS) inboxTypingHeld(it session.AttentionItem, now time.Time) (bool, time.Time) {
	ed := m.Inbox.reply.editor
	if ed == nil || len(ed.arrived) == 0 {
		return false, time.Time{}
	}
	a, ok := ed.arrived[it.ID]
	if !ok || a.key != inboxShownKey(it) {
		return false, time.Time{}
	}
	return ed.typing.Held(a.at, now, m.inboxReplyFocused())
}

func (m *OS) inboxTypingRefused(it session.AttentionItem) bool {
	if held, _ := m.inboxTypingHeld(it, time.Now()); !held {
		return false
	}
	m.ShowNotification(inboxTypingWords+". Stop typing, then answer", "warning", m.Settings.NotificationDuration)
	return true
}

func (m *OS) armInboxTypingRelease() tea.Cmd {
	ed := m.Inbox.reply.editor
	if ed == nil || ed.holdArmed || len(ed.arrived) == 0 {
		return nil
	}
	now := time.Now()
	var release time.Time
	for _, it := range m.Inbox.Items {
		if held, at := m.inboxTypingHeld(it, now); held {
			release = at
			break
		}
	}
	if release.IsZero() {
		return nil
	}
	ed.holdArmed = true
	st := &m.Inbox.reply
	st.holdSeq++
	seq := st.holdSeq
	return tea.Tick(release.Sub(now), func(time.Time) tea.Msg { return inboxTypingReleaseMsg{seq: seq} })
}

func (m *OS) applyInboxTypingRelease(msg inboxTypingReleaseMsg) tea.Cmd {
	m.cockpitTouch()
	if msg.seq != m.Inbox.reply.holdSeq {
		return nil
	}
	if ed := m.Inbox.reply.editor; ed != nil {
		ed.holdArmed = false
	}
	return m.armInboxTypingRelease()
}
