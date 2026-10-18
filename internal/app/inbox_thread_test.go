package app

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestCockpitAnswersOnlyWhatItShowed: in the cockpit a answers the approval
// the selected agent waits on, as the person, and only once its card has
// shown it whole for the settle time. A key that lands before the card was
// drawn, or too soon after, sends nothing. It is the Inbox's rule, and the
// letters must not be a way around it.
func TestCockpitAnswersOnlyWhatItShowed(t *testing.T) {
	m, q := replyOS(t)
	m.Windows[1].AgentState, m.Windows[1].AgentHarness = "needs_input", "claude-code"
	held := heldApproval("7", "r7", session.ApprovalOnce, session.ApprovalDeny)
	held.Session, held.Window = "here", "w-2"
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{held}})
	m.OpenInbox("")
	e, ok := m.cockpitAgent()
	if !ok || e.WindowID != "w-2" {
		t.Fatalf("the agent waiting on you is not selected: %+v", e)
	}

	// Not drawn yet: nothing is sent.
	if cmd, _ := m.CockpitAnswerLetter("a"); cmd != nil {
		runMsg(t, m, cmd)
	}
	if _, sent := q.last("reply-approval"); sent {
		t.Fatal("an approval the thread never showed was answered")
	}

	// Drawn just now: still too soon.
	m.renderInbox()
	if cmd, _ := m.CockpitAnswerLetter("a"); cmd != nil {
		runMsg(t, m, cmd)
	}
	if _, sent := q.last("reply-approval"); sent {
		t.Fatal("an approval drawn a moment ago was answered")
	}

	// Shown for the settle time: 1 allows once, for that request.
	m.Inbox.shown.since = time.Now().Add(-time.Second)
	cmd, _ := m.CockpitAnswerLetter("a")
	if cmd == nil {
		t.Fatalf("a settled approval was not answered: %q", lastNotice(m).Message)
	}
	cmd()
	p, sent := q.last("reply-approval")
	if !sent || p["request_id"] != "r7" || p["decision"] != session.ApprovalOnce || p["human_nonce"] != "nonce-1" {
		t.Fatalf("reply-approval = %v (sent %v)", p, sent)
	}
}
