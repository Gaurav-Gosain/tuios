package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The typing hold: an approval or a question that arrives, or changes, while
// the person types a reply is not answered by a click or a key until they
// pause. composer.TypingHold is the rule. This file feeds it the times and
// asks it, and the card draws a held item with the words inboxTypingWords
// instead of its buttons.
//
// It costs nothing at idle. A key into the reply records a time. An item that
// arrives while the reply editor is open records a time. Only while something
// is held is one tea.Tick out, for the release time, and it re-arms itself
// only while something is still held.

// inboxTypingWords is what a held item shows in place of its buttons, and
// what an answer to it says.
const inboxTypingWords = "Paused while you type"

// inboxArrival is when this client saw an item, as it is now, arrive while the
// reply editor was open.
type inboxArrival struct {
	key string
	at  time.Time
}

// inboxTypingReleaseMsg is the one-shot redraw at the release time of a hold.
type inboxTypingReleaseMsg struct{ seq uint64 }

// inboxTypingKind reports whether an item is one a key or a click answers
// here: a held approval or plan, or a question ask-human put.
func inboxTypingKind(it session.AttentionItem) bool {
	return inboxHeld(it) || (it.Kind == session.AttentionAsk && it.RequestID != "")
}

// inboxReplyFocused reports whether the reply editor has the keyboard: it is
// open, it writes to the row under the cursor, and nothing drawn over it
// takes the keys first.
func (m *OS) inboxReplyFocused() bool {
	return m.InboxReplyOpen() && !m.CockpitImageOpen() && !m.CockpitSpawnOpen() &&
		!m.CockpitCompareOpen() && !m.CockpitArtifactFocused() && !m.InboxPeeking() &&
		!m.cockpitReplyMismatch()
}

// noteInboxTyped records a key typed into the reply editor. A key from
// send-keys is not counted, so an agent cannot keep its own approval held.
func (m *OS) noteInboxTyped() {
	if ed := m.Inbox.reply.editor; ed != nil && !m.ProcessingRemoteKeys {
		ed.typing.Key(time.Now())
	}
}

// noteInboxArrival records when an item arrived or changed, while the reply
// editor is open. prev is the item as the mirror held it before, nil when it
// is new. An item that was there before the editor opened has no entry, and is
// never held.
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

// inboxTypingHeld reports whether the typing hold holds it at now, and when
// it releases.
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

// inboxTypingRefused refuses an answer to an item the typing hold holds, with
// a word. It reports whether it refused.
func (m *OS) inboxTypingRefused(it session.AttentionItem) bool {
	if held, _ := m.inboxTypingHeld(it, time.Now()); !held {
		return false
	}
	m.ShowNotification(inboxTypingWords+". Stop typing, then answer", "warning", m.Settings.NotificationDuration)
	return true
}

// armInboxTypingRelease schedules the redraw at the release time of the
// typing hold, when something is held and no redraw is out yet. It does
// nothing when nothing is held.
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

// applyInboxTypingRelease redraws at the release time. A later key moved the
// release on, so it arms again while something is still held. A redraw from
// an editor since closed only redraws.
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
