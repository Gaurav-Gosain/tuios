package app

import (
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

type inboxQuestionView struct {
	itemID, summary string
	loading         bool
	peek            *session.PromptPeek
	err             string
}

// InboxQuestionPeekMsg is a peek-prompt reply for the question view.
type InboxQuestionPeekMsg struct {
	ItemID, Summary string
	Peek            *session.PromptPeek
	Err             error
}

func inboxAsksInPane(it session.AttentionItem) bool {
	return it.Kind == session.AttentionQuestion && it.RequestID == "" && it.Window != "" && it.Host == ""
}

// InboxQuestionFetch reads the prompt of the question under the cursor, once
// per item and question.
func (m *OS) InboxQuestionFetch() tea.Cmd {
	if !m.ShowInbox || m.Inbox.Peek != nil {
		return nil
	}
	it, ok := m.inboxSelected()
	if !ok || !inboxAsksInPane(it) {
		return nil
	}
	qv := &m.Inbox.approvals.question
	if qv.itemID == it.ID && qv.summary == it.Summary {
		return nil
	}
	if m.DaemonClient == nil && m.Inbox.call == nil {
		return nil
	}
	*qv = inboxQuestionView{itemID: it.ID, summary: it.Summary, loading: true}
	call, id, sum := m.inboxActCaller(), it.ID, it.Summary
	params := map[string]any{"session": it.Session, "window": it.Window}
	return func() tea.Msg {
		raw, err := call("peek-prompt", params, inboxPeekTimeout)
		if err != nil {
			return InboxQuestionPeekMsg{ItemID: id, Summary: sum, Err: err}
		}
		var pk session.PromptPeek
		if err := jsonx.Unmarshal(raw, &pk); err != nil {
			return InboxQuestionPeekMsg{ItemID: id, Summary: sum, Err: err}
		}
		return InboxQuestionPeekMsg{ItemID: id, Summary: sum, Peek: &pk}
	}
}

const inboxQuestionRetry = 500 * time.Millisecond

type inboxQuestionRetryMsg struct{ itemID, summary string }

func (m *OS) applyInboxQuestionPeek(msg InboxQuestionPeekMsg) tea.Cmd {
	qv := &m.Inbox.approvals.question
	if qv.itemID != msg.ItemID || qv.summary != msg.Summary {
		return nil
	}
	m.cockpitTouch()
	qv.loading = false
	if msg.Err != nil {
		qv.err = msg.Err.Error()
		return nil
	}
	qv.peek = msg.Peek
	if msg.Peek != nil && msg.Peek.Found && len(msg.Peek.Options) > 0 {
		return nil
	}
	id, sum := msg.ItemID, msg.Summary
	return tea.Tick(inboxQuestionRetry, func(time.Time) tea.Msg { return inboxQuestionRetryMsg{itemID: id, summary: sum} })
}

func (m *OS) applyInboxQuestionRetry(msg inboxQuestionRetryMsg) tea.Cmd {
	qv := &m.Inbox.approvals.question
	if qv.itemID != msg.itemID || qv.summary != msg.summary || qv.loading {
		return nil
	}
	if qv.peek != nil && qv.peek.Found && len(qv.peek.Options) > 0 {
		return nil
	}
	*qv = inboxQuestionView{}
	return m.InboxQuestionFetch()
}

func (m *OS) inboxQuestionOptions(it session.AttentionItem) []harness.Option {
	qv := &m.Inbox.approvals.question
	if !inboxAsksInPane(it) || qv.itemID != it.ID || qv.summary != it.Summary || qv.peek == nil || !qv.peek.Found {
		return nil
	}
	if !qv.peek.Offers(harness.ActionChoose) {
		return nil
	}
	return qv.peek.Options
}

func (m *OS) inboxQuestionChoose(it session.AttentionItem, n int) (tea.Cmd, bool) {
	opts := m.inboxQuestionOptions(it)
	if opts == nil {
		if inboxAsksInPane(it) {
			m.ShowNotification("The options of this question are not read yet. Space reads the prompt", "info", m.Settings.NotificationDuration)
			return nil, true
		}
		return nil, false
	}
	found := false
	for _, o := range opts {
		if o.N == n {
			found = true
		}
	}
	if !found {
		m.ShowNotification("No option "+strconv.Itoa(n)+" is on the screen", "warning", m.Settings.NotificationDuration)
		return nil, true
	}
	if m.refuseRemoteAnswer("An answer") {
		return nil, true
	}
	m.Inbox.peekGen++
	m.Inbox.Peek = &inboxPeek{Item: it, Peek: m.Inbox.approvals.question.peek, gen: m.Inbox.peekGen}
	m.InboxPeekAsk(harness.ActionChoose, strconv.Itoa(n))
	m.cockpitTouch()
	return nil, true
}
