package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/x/ansi"
)

// inboxOS is a client attached to session "here", with two panes, a live
// Inbox and the given alert policy.
func inboxOS(t *testing.T, agent config.AgentAlertsConfig) *OS {
	t.Helper()
	m := alertOS(t, agent)
	m.SessionName = "here"
	m.IsDaemonSession = true
	m.Inbox.Live = true
	m.WorkspaceFocus = map[int]int{}
	return m
}

func item(id, kind, sess, window, summary string, since int64) session.AttentionItem {
	return session.AttentionItem{ID: id, Kind: kind, Session: sess, Window: window, Name: "agent-" + id, Summary: summary, Since: since}
}

func opened(items ...session.AttentionItem) InboxEventsMsg {
	var evs []InboxEvent
	for i := range items {
		evs = append(evs, InboxEvent{Action: session.AttentionOpened, Item: &items[i]})
	}
	return InboxEventsMsg{Events: evs}
}

func TestInboxMirrorFollowsEvents(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{
		item("1", session.AttentionFinished, "a", "w1", "", 10),
		item("2", session.AttentionApproval, "a", "w2", "", 20),
	}})
	if m.Inbox.Items[0].ID != "2" {
		t.Fatalf("the snapshot is not in Inbox order: %+v", m.Inbox.Items)
	}
	gen := m.Inbox.Gen

	q := item("3", session.AttentionQuestion, "b", "w3", "which?", 5)
	m.applyInboxEvents(opened(q))
	closed := item("2", session.AttentionApproval, "a", "w2", "", 20)
	m.applyInboxEvents(InboxEventsMsg{Events: []InboxEvent{{Action: session.AttentionClosed, Item: &closed}}})
	var ids []string
	for _, it := range m.Inbox.Items {
		ids = append(ids, it.ID)
	}
	if got := strings.Join(ids, ","); got != "3,1" {
		t.Errorf("mirror holds %s, want 3,1", got)
	}
	if m.Inbox.Gen <= gen {
		t.Error("an event did not bump the generation the rail's cache keys on")
	}

	m.applyInboxDown(InboxDownMsg{})
	if m.Inbox.Live {
		t.Error("losing the daemon left the mirror marked live")
	}
}

// TestInboxAlertsForOtherSessionsOnly is the gap the Inbox closes: an agent in
// a session this client is not attached to raised nothing. It now raises one
// dock message naming the session and the summary, and the attached session
// is left to the state sync, which already alerts on it.
func TestInboxAlertsForOtherSessionsOnly(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	host := captureHost(t, m)

	m.applyInboxEvents(opened(item("1", session.AttentionApproval, "here", "w-1", "approve Bash", 1)))
	if len(m.Notifications) != 0 {
		t.Fatalf("an item in the attached session raised %d dock messages; the state sync owns those", len(m.Notifications))
	}

	m.applyInboxEvents(opened(item("2", session.AttentionQuestion, "fan-3", "x", "which branch?", 2)))
	if len(m.Notifications) != 1 {
		t.Fatalf("an item in another session raised %d dock messages, want 1", len(m.Notifications))
	}
	n := m.Notifications[0]
	if !strings.Contains(n.Message, "fan-3") || !strings.Contains(n.Message, "which branch?") {
		t.Errorf("the dock text %q does not name the session and the summary", n.Message)
	}
	if n.Target == nil || n.Target.SessionID != "fan-3" || n.Target.WindowID != "x" {
		t.Errorf("the dock message targets %+v, want fan-3/x", n.Target)
	}
	if !strings.Contains(host.b.String(), "\x1b]9;") {
		t.Errorf("no desktop notification was written: %q", host.b.String())
	}
}

// TestInboxBurstIsOneAlert is a fan finishing together: one dock message that
// counts, not one per agent.
func TestInboxBurstIsOneAlert(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	captureHost(t, m)
	var items []session.AttentionItem
	for i, s := range []string{"fan-1", "fan-2", "fan-3", "fan-4", "fan-5"} {
		items = append(items, item(string(rune('a'+i)), session.AttentionApproval, s, "w", "", int64(i)))
	}
	m.applyInboxEvents(opened(items...))
	if len(m.Notifications) != 1 {
		t.Fatalf("a burst of 5 raised %d dock messages, want 1", len(m.Notifications))
	}
	if got := m.Notifications[0].Message; !strings.HasPrefix(got, "5 agents need you in fan-1, fan-2, fan-3 and 2 more") {
		t.Errorf("burst text %q", got)
	}
}

// TestInboxAlertWaitsOutTheSettleWindow: an item that closes inside the window
// raises nothing, one that stays raises its alert when the window ends.
func TestInboxAlertWaitsOutTheSettleWindow(t *testing.T) {
	m := inboxOS(t, config.AgentAlertsConfig{})
	captureHost(t, m)
	flicker := item("1", session.AttentionApproval, "other", "w", "", 1)
	stays := item("2", session.AttentionErrored, "other", "v", "boom", 2)
	cmd := m.applyInboxEvents(opened(flicker, stays))
	if cmd == nil {
		t.Fatal("no settle timer was scheduled")
	}
	if len(m.Notifications) != 0 {
		t.Fatal("an alert fired before the settle window ended")
	}
	m.applyInboxEvents(InboxEventsMsg{Events: []InboxEvent{{Action: session.AttentionClosed, Item: &flicker}}})
	m.applyInboxAlertDue(InboxAlertDueMsg{IDs: []string{"1", "2"}})
	if len(m.Notifications) != 1 || !strings.Contains(m.Notifications[0].Message, "boom") {
		t.Fatalf("after the window: %+v, want one alert for the item that stayed", m.Notifications)
	}
}

func TestInboxAlertHonoursThePolicy(t *testing.T) {
	off := false
	m := inboxOS(t, config.AgentAlertsConfig{Enabled: &off})
	m.applyInboxEvents(opened(item("1", session.AttentionApproval, "other", "w", "", 1)))
	if len(m.Notifications) != 0 {
		t.Error("alerts off still raised a dock message")
	}
	// finished is on by default, and a state switched off stays silent.
	m = inboxOS(t, zeroSettle())
	m.UserConfig.Notifications.Agent.States.Done = &off
	m.applyInboxEvents(opened(item("2", session.AttentionFinished, "other", "w", "", 1)))
	if len(m.Notifications) != 0 {
		t.Error("done switched off still raised a dock message for a finished item")
	}
}

func TestInboxRowsGroupAndSkipHeadings(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{
		item("1", session.AttentionApproval, "a", "w1", "", 30),
		item("2", session.AttentionApproval, "a", "w2", "", 10),
		item("3", session.AttentionFinished, "b", "w3", "", 5),
	}})
	m.OpenInbox("")
	rows := m.inboxRows()
	if len(rows) != 5 || rows[0].heading != "Approvals" || rows[0].count != 2 || rows[3].heading != "Done" {
		t.Fatalf("rows %+v", rows)
	}
	if it, _ := m.inboxSelected(); it.ID != "2" {
		t.Errorf("opening selected %s, want the oldest approval 2", it.ID)
	}
	m.InboxMove(1)
	m.InboxMove(1)
	if it, _ := m.inboxSelected(); it.ID != "3" {
		t.Errorf("two moves down landed on %s, want 3 (over the heading)", it.ID)
	}
	// One step past the end wraps to the first item, over the heading at the
	// top (appearance.wrap_lists), and a step back up wraps back.
	m.InboxMove(1)
	if it, _ := m.inboxSelected(); it.ID != "2" {
		t.Errorf("moving past the end left %s selected, want the first item 2", it.ID)
	}
	m.InboxMove(-1)
	if it, _ := m.inboxSelected(); it.ID != "3" {
		t.Errorf("moving up from the first item left %s selected, want the last item 3", it.ID)
	}
	m.InboxMove(10)
	if it, _ := m.inboxSelected(); it.ID != "3" {
		t.Errorf("a page past the end left %s selected, want it to stop on 3", it.ID)
	}
	m.InboxMove(-10)
	if it, _ := m.inboxSelected(); it.ID != "2" {
		t.Errorf("moving to the top landed on %s", it.ID)
	}

	// Ask is stepped over: Questions shows both kinds of question.
	for _, want := range []string{session.AttentionApproval, session.AttentionPlan, session.AttentionQuestion, session.AttentionMail, session.AttentionErrored, session.AttentionResume, session.AttentionFinished, session.AttentionOutbox, ""} {
		m.InboxCycleFilter()
		if m.Inbox.Filter != want {
			t.Fatalf("filter %q, want %q", m.Inbox.Filter, want)
		}
	}
	m.Inbox.Filter = session.AttentionFinished
	if rows := m.inboxRows(); len(rows) != 2 || rows[1].item.ID != "3" {
		t.Errorf("the finished filter shows %+v", rows)
	}
}

// TestInboxQuestionsAreOneGroup: a question an agent's prompt asks and one put
// with ask-human sit under one Questions heading, oldest first across both.
// They were two groups, "Questions" and "Asked you".
func TestInboxQuestionsAreOneGroup(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{
		item("1", session.AttentionAsk, "a", "w1", "", 20),
		item("2", session.AttentionQuestion, "a", "w2", "", 10),
		item("3", session.AttentionQuestion, "a", "w3", "", 30),
	}})
	m.OpenInbox("")
	rows := m.inboxRows()
	if len(rows) != 4 || rows[0].heading != "Questions" || rows[0].count != 3 {
		t.Fatalf("rows %+v, want one Questions heading over three items", rows)
	}
	var order []string
	for _, r := range rows[1:] {
		order = append(order, r.item.ID)
	}
	if strings.Join(order, ",") != "2,1,3" {
		t.Errorf("questions in order %v, want oldest first across both kinds: 2,1,3", order)
	}
	plain := ansi.Strip(func() string { s, _, _ := m.renderInbox(); return s }())
	if strings.Contains(plain, "Asked you") {
		t.Errorf("the Inbox still draws an Asked you group:\n%s", plain)
	}

	m.Inbox.Filter = session.AttentionQuestion
	if rows := m.inboxRows(); len(rows) != 4 {
		t.Errorf("the Questions filter shows %d rows, want both kinds", len(rows))
	}
	m.Inbox.Filter = session.AttentionAsk
	if rows := m.inboxRows(); len(rows) != 2 || rows[1].item.ID != "1" {
		t.Errorf("a filter on ask-human questions alone shows %+v", rows)
	}
}

// TestNextAttentionCyclesOldestFirst: the key goes to the oldest item that
// needs the person, then the next, and around again; finished turns are not
// visited.
func TestNextAttentionCyclesOldestFirst(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{
		item("1", session.AttentionQuestion, "here", "w-1", "", 5),
		item("2", session.AttentionApproval, "here", "w-2", "", 50),
		item("3", session.AttentionFinished, "here", "w-1", "", 1),
	}})
	var got []string
	for range 3 {
		m.JumpToNextAttention()
		got = append(got, m.GetFocusedWindow().ID)
	}
	if strings.Join(got, ",") != "w-2,w-1,w-2" {
		t.Errorf("jumps went %v, want w-2 (the approval), w-1, then around to w-2", got)
	}

	m.Inbox.Items = m.Inbox.Items[2:]
	m.Notifications = nil
	m.JumpToNextAttention()
	if len(m.Notifications) != 1 || !strings.Contains(m.Notifications[0].Message, "Nothing is waiting") {
		t.Errorf("with only a finished turn left, the key said %+v", m.Notifications)
	}
}

// TestInboxReplyOpensTheThreadWithItsReplyLine covers r on mail in this
// session, and the path a thread in another session takes once that session's
// mail has loaded.
func TestInboxReplyOpensTheThreadWithItsReplyLine(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	m.DaemonClient = &session.TUIClient{}
	m.AgentMail.Messages = []session.AgentMessage{{
		ID: 3, Kind: "message", From: "w-1", FromLabel: "w-1", To: session.AgentInboxHuman, Subject: "ship it?", ThreadID: 3, SentAt: 1,
	}}
	mailItem := item("5", session.AttentionMail, "here", "w-1", "ship it?", 1)
	mailItem.Thread = 3
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{mailItem, item("6", session.AttentionErrored, "here", "w-2", "", 1)}})
	m.OpenInbox("")
	m.InboxMove(1)
	m.InboxReply()
	if !m.ShowInbox {
		t.Fatal("r on an errored item left the Inbox")
	}
	m.InboxMove(-1)
	m.InboxReply()
	if m.ShowInbox || !m.ShowAgentMail || m.AgentMail.Thread != 3 || !m.AgentMail.Composing {
		t.Fatalf("r on mail: inbox=%v mail=%v thread=%d composing=%v", m.ShowInbox, m.ShowAgentMail, m.AgentMail.Thread, m.AgentMail.Composing)
	}

	m.CloseAgentMail()
	m.Inbox.pendingThread, m.Inbox.pendingReply = 3, true
	m.takePendingInboxThread()
	if !m.ShowAgentMail || m.AgentMail.Thread != 3 || !m.AgentMail.Composing || m.Inbox.pendingThread != 0 {
		t.Errorf("a pending thread did not open with its reply line once the mail loaded")
	}
}

func TestSidebarHeaderCountsTheInbox(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	rows := []sidebarAgentEntry{{State: "needs_input"}, {State: "needs_input"}, {State: "done", Host: "box", SessionID: "r"}}
	m.Inbox.Live = false
	if c := m.sidebarHeaderCounts(rows); c.Blocked != 2 || c.Done != 1 {
		t.Errorf("without a live Inbox the header counts rows: %+v", c)
	}
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{
		item("1", session.AttentionErrored, "here", "w1", "", 1),
		item("2", session.AttentionApproval, "elsewhere", "w1", "", 1),
		item("3", session.AttentionFinished, "elsewhere", "w1", "", 1),
		item("4", session.AttentionMail, "here", "w1", "", 1),
	}})
	c := m.sidebarHeaderCounts(rows)
	// Two blocked from the Inbox, one done from it, one done from the row on
	// the other machine.
	// errored outranks needs_input on the rail, so it is the worst.
	if c.Blocked != 2 || c.Done != 2 || c.Worst != "errored" {
		t.Errorf("with a live Inbox the header counts %+v, want 2 blocked, 2 done, worst errored", c)
	}
	m.SidebarAgentFilter = sidebarAgentsSession
	if c := m.sidebarHeaderCounts(nil); c.Blocked != 1 || c.Done != 0 {
		t.Errorf("filtered to here the header counts %+v", c)
	}
}

// A dismiss the person did not ask for, and one that lost a race to another
// client or to the daemon's own close, show nothing. A dismiss the person
// asked for that really failed still says so.
func TestInboxDismissFailuresOnlyShowWhenTheyMatter(t *testing.T) {
	gone := &session.VerbCallError{
		Code:    session.ErrVerbInvalidParams,
		Message: "no open attention item has id 4",
		Hint:    &session.VerbHint{Param: "id"},
	}
	notHuman := &session.VerbCallError{
		Code:    session.ErrVerbNotHuman,
		Message: "dismiss-attention is for the person at an attached client",
		Hint:    &session.VerbHint{Param: "human_nonce"},
	}
	transport := errors.New("failed to send request: broken pipe")
	cases := []struct {
		name string
		msg  InboxDismissedMsg
		want int
	}{
		{"silent and already closed", InboxDismissedMsg{Err: gone, Silent: true}, 0},
		{"silent and broken", InboxDismissedMsg{Err: transport, Silent: true}, 0},
		{"asked for and already closed", InboxDismissedMsg{Err: gone}, 0},
		{"asked for and not human", InboxDismissedMsg{Err: notHuman}, 1},
		{"asked for and broken", InboxDismissedMsg{Err: transport}, 1},
		{"asked for and done", InboxDismissedMsg{}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := inboxOS(t, zeroSettle())
			m.applyInboxDismissed(c.msg)
			if len(m.Notifications) != c.want {
				t.Errorf("%d toasts, want %d", len(m.Notifications), c.want)
			}
		})
	}
}
