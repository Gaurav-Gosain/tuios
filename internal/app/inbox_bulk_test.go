package app

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// bulkOS is the agents view over three agents, each waiting on a held
// approval: a plain one, a risky one, and a plan. All three are marked.
func bulkOS(t *testing.T) (*OS, *queueFake) {
	t.Helper()
	m, q := replyOS(t)
	m.Windows = append(m.Windows, &terminal.Window{ID: "w-3", CustomName: "w-3", Workspace: 1})
	for _, w := range m.Windows {
		w.AgentState, w.AgentHarness = "needs_input", "claude-code"
	}
	plain := heldApproval("1", "r1", session.ApprovalOnce, session.ApprovalAlways, session.ApprovalDeny)
	risky := heldApproval("2", "r2", session.ApprovalOnce, session.ApprovalDeny)
	risky.Risk = []string{"rm_rf"}
	plan := heldApproval("3", "r3", session.ApprovalOnce, session.ApprovalDeny)
	plan.Kind = session.AttentionPlan
	items := []session.AttentionItem{plain, risky, plan}
	for i := range items {
		items[i].Session = "here"
	}
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: items})
	m.OpenInbox("")
	m.Inbox.agents.on = true
	m.InboxAgentsMarkAll()
	if m.InboxAgentsMarks() != 3 {
		t.Fatalf("marked %d agents, want 3", m.InboxAgentsMarks())
	}
	return m, q
}

// sentApprovals are the reply-approval calls made, by request id.
func sentApprovals(q *queueFake) map[string]string {
	out := map[string]string{}
	for _, c := range q.calls {
		if c.verb == "reply-approval" {
			out[c.params["request_id"].(string)] = c.params["decision"].(string)
		}
	}
	return out
}

// TestBulkAllowLeavesOutWhatNeedsASecondLook: a bulk allow lists each
// approval it would allow, allows only after the list has been on screen for
// the settle time, never takes a risky approval or a plan, and skips one that
// changed after the list showed it.
func TestBulkAllowLeavesOutWhatNeedsASecondLook(t *testing.T) {
	m, q := bulkOS(t)
	m.InboxAgentsBulkAllow()
	sheet := m.Inbox.agents.sheet
	if sheet == nil || len(sheet.allow) != 1 || sheet.allow[0].item.RequestID != "r1" || len(sheet.held) != 2 {
		t.Fatalf("sheet = %+v, want r1 to allow and two held back", sheet)
	}
	m.bulkSheetLines(80, 30)
	// Too soon: nothing goes, and the sheet stays.
	if cmd := m.InboxAgentsSheetKey("enter"); cmd != nil || m.Inbox.agents.sheet == nil {
		t.Fatal("enter allowed before the list was on screen for the settle time")
	}
	sheet.at = time.Now().Add(-time.Second)
	runCmds(m.InboxAgentsSheetKey("enter"))
	if got := sentApprovals(q); len(got) != 1 || got["r1"] != session.ApprovalOnce {
		t.Fatalf("allowed %v, want r1 once and nothing else", got)
	}

	// An approval that changed after the list showed it is not allowed.
	m, q = bulkOS(t)
	m.InboxAgentsBulkAllow()
	m.bulkSheetLines(80, 30)
	m.Inbox.agents.sheet.at = time.Now().Add(-time.Second)
	for i := range m.Inbox.Items {
		if m.Inbox.Items[i].RequestID == "r1" {
			m.Inbox.Items[i].Summary = "approve Bash: rm -rf /"
		}
	}
	runCmds(m.InboxAgentsSheetKey("enter"))
	if got := sentApprovals(q); len(got) != 0 {
		t.Fatalf("an approval that changed after the list was allowed: %v", got)
	}
}

// TestBulkAllowTakesOnlyWhatTheScreenShowed: on a screen with room for one
// approval, the sheet draws one, and enter allows that one and not the two
// the screen left out.
func TestBulkAllowTakesOnlyWhatTheScreenShowed(t *testing.T) {
	m, q := bulkOS(t)
	for i := range m.Inbox.Items {
		m.Inbox.Items[i].Risk, m.Inbox.Items[i].Kind = nil, session.AttentionApproval
	}
	m.InboxAgentsBulkAllow()
	if n := len(m.Inbox.agents.sheet.allow); n != 3 {
		t.Fatalf("the sheet holds %d approvals, want 3", n)
	}
	lines := m.bulkSheetLines(80, 5)
	if len(lines) > 5 && m.Inbox.agents.sheet.shown > 1 {
		t.Fatalf("drew %d lines in 5 rows and %d approvals", len(lines), m.Inbox.agents.sheet.shown)
	}
	m.Inbox.agents.sheet.at = time.Now().Add(-time.Second)
	runCmds(m.InboxAgentsSheetKey("enter"))
	if got := sentApprovals(q); len(got) != 1 || got["r1"] != session.ApprovalOnce {
		t.Fatalf("allowed %v, want r1 alone: the others were not on the screen", got)
	}
}

// TestBulkActionsFromSendKeysAreRefused: a key send-keys typed allows,
// denies and replies to nothing in bulk.
func TestBulkActionsFromSendKeysAreRefused(t *testing.T) {
	m, q := bulkOS(t)
	m.ProcessingRemoteKeys = true
	runCmds(m.InboxAgentsBulkDeny())
	m.InboxAgentsBulkAllow()
	runCmds(m.InboxAgentsBulkReply())
	m.ProcessingRemoteKeys = false
	if len(q.calls) != 0 || m.Inbox.agents.sheet != nil || m.Inbox.reply.editor != nil {
		t.Fatalf("send-keys acted in bulk: calls %v, sheet %v, editor %v", q.calls, m.Inbox.agents.sheet != nil, m.Inbox.reply.editor != nil)
	}
}

// TestBulkReplyGoesToEach: one reply, queued to each marked agent that is
// not waiting on a prompt, with the same text.
func TestBulkReplyGoesToEach(t *testing.T) {
	m, q := bulkOS(t)
	m.Windows[0].AgentState, m.Windows[2].AgentState = "done", "working"
	m.Inbox.Gen++
	runCmds(m.InboxAgentsBulkReply())
	if m.Inbox.reply.editor == nil || len(m.Inbox.reply.editor.targets) != 2 {
		t.Fatalf("the bulk editor has %+v", m.Inbox.reply.editor)
	}
	m.InboxReplyType("rebase on main")
	runCmds(m.InboxReplySend())
	n := 0
	for _, c := range q.calls {
		if c.verb == "queue-prompt" {
			n++
			if c.params["text"] != "rebase on main" || c.params["human_nonce"] != "nonce-1" {
				t.Errorf("queue-prompt %v", c.params)
			}
		}
	}
	if n != 2 {
		t.Fatalf("queued %d replies, want 2: %v", n, q.calls)
	}
}

// runCmds runs a command and every command a batch holds, and drops what
// they return.
func runCmds(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if b, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range b {
			runCmds(c)
		}
	}
}
