package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/risk"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/x/ansi"
)

// replyDaemon answers reply-approval and get-approval and records the calls.
type replyDaemon struct {
	calls []fakeCall
	plan  string
	sha   string
}

func (r *replyDaemon) call(verb string, params map[string]any, _ time.Duration) (json.RawMessage, error) {
	r.calls = append(r.calls, fakeCall{verb, params})
	switch verb {
	case "reply-approval":
		return json.Marshal(map[string]any{"decision": params["decision"], "applied": true, "reason": "answered"})
	case "get-approval":
		return json.Marshal(session.ApprovalDetail{RequestID: fmt.Sprint(params["request_id"]), Kind: session.AttentionPlan, Plan: r.plan, PlanSHA: r.sha})
	}
	return nil, &session.VerbCallError{Code: session.ErrVerbUnknownVerb, Message: verb}
}

func (r *replyDaemon) replies() []map[string]any {
	var out []map[string]any
	for _, c := range r.calls {
		if c.verb == "reply-approval" {
			out = append(out, c.params)
		}
	}
	return out
}

// riskyHeld is a held approval a risk rule matched.
func riskyHeld() session.AttentionItem {
	it := heldApproval("1", "r1", session.ApprovalOnce, session.ApprovalAlways, session.ApprovalDeny)
	it.Summary = "approve Bash: rm -rf build/"
	it.AlwaysScope = []string{"Bash(rm:*) in .claude/settings.local.json"}
	it.Risk = []string{risk.RuleRecursiveDelete}
	it.DenyMessage = true
	return it
}

// approvalsOS is a client with items in its Inbox, the first selected, drawn
// once and settled, and a fake daemon behind it.
func approvalsOS(t *testing.T, items ...session.AttentionItem) (*OS, *replyDaemon) {
	t.Helper()
	m := inboxOS(t, zeroSettle())
	m.Height, m.Width = 24, 80
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: items})
	m.OpenInbox("")
	r := &replyDaemon{}
	m.SetInboxVerbCaller(r.call, func() string { return "nonce-1" })
	m.renderInbox()
	settleShown(m)
	return m, r
}

// press is one key in the Inbox list the way the input path delivers it.
func press(m *OS, key string) {
	m.InboxKeyPressed(key)
}

func drawn(m *OS) string {
	out, _, _ := m.renderInbox()
	return ansi.Strip(out)
}

// TestRiskyPressResets: a second press after the window, after another key,
// or on another key is a first press again.
func TestRiskyPressResets(t *testing.T) {
	m, r := approvalsOS(t, riskyHeld())

	press(m, "1")
	m.InboxNumber(1)
	m.Inbox.approvals.armed.at = time.Now().Add(-inboxRiskPressWindow - time.Second)
	press(m, "1")
	if cmd := m.InboxNumber(1); cmd != nil {
		t.Error("a second press after the window sent the allow")
	}

	press(m, "j")
	press(m, "1")
	if cmd := m.InboxNumber(1); cmd != nil {
		t.Error("a press after another key sent the allow")
	}

	press(m, "2")
	if cmd := m.InboxNumber(2); cmd != nil {
		t.Error("2 after an armed 1 sent always")
	}
	if len(r.replies()) != 0 {
		t.Fatalf("replies %v", r.replies())
	}
}

// planItem is a held plan of n lines.
func planItem(lines int) (session.AttentionItem, string) {
	var b strings.Builder
	b.WriteString("# Refactor the retry loop\n")
	for i := 1; i < lines; i++ {
		fmt.Fprintf(&b, "%d. step %d\n", i, i)
	}
	text := b.String()
	it := item("5", session.AttentionPlan, "web", "w-5", "plan: Refactor the retry loop", time.Now().Add(-5*time.Minute).UnixNano())
	it.Name = "planner"
	it.Harness = "claude-code"
	it.RequestID = "p5"
	it.Options = []string{session.ApprovalOnce, session.ApprovalAlways, session.ApprovalDeny}
	it.AlwaysScope = []string{"Mode accept edits, for this session"}
	it.PlanLines = lines
	it.PlanSHA = "sha-" + fmt.Sprint(lines)
	it.DenyMessage = true
	return it, text
}

// fetchPlan runs the plan read and feeds its reply back.
func fetchPlan(t *testing.T, m *OS) {
	t.Helper()
	cmd := m.InboxApprovalFetch()
	if cmd == nil {
		t.Fatal("the plan under the cursor was not read")
	}
	m.applyInboxApprovalDetail(cmd().(InboxApprovalDetailMsg))
	if m.InboxApprovalFetch() != nil {
		t.Error("a plan already read is read again")
	}
}

// TestPlanApprovesOnlyOnceReadToTheEnd: on a plan longer than the detail, 1
// does nothing until its last line has been on screen; keep planning works at
// once.
//
// Negative control: with the seenEnd check removed from inboxApprovalGate, the
// first 1 sends the approve.
func TestPlanApprovesOnlyOnceReadToTheEnd(t *testing.T) {
	it, text := planItem(30)
	m, r := approvalsOS(t, it)
	r.plan, r.sha = text, it.PlanSHA
	fetchPlan(t, m)
	plain := drawn(m)
	if !strings.Contains(plain, "lines 1-8 of 30") || !strings.Contains(plain, "22 more below") {
		t.Fatalf("a long plan at 80x24 is not windowed:\n%s", plain)
	}
	settleShown(m)
	press(m, "1")
	if cmd := m.InboxNumber(1); cmd != nil {
		t.Fatal("ASSERTION: 1 approved a plan whose end was never shown")
	}
	if !strings.Contains(lastNote(m), "Read the plan to its end") {
		t.Errorf("note %q", lastNote(m))
	}
	for range 30 {
		m.InboxDetailScroll(1)
	}
	plain = drawn(m)
	if !strings.Contains(plain, "lines 23-30 of 30") || !strings.Contains(plain, "29. step 29") {
		t.Fatalf("scrolling did not reach the end:\n%s", plain)
	}
	settleShown(m)
	press(m, "1")
	cmd := m.InboxNumber(1)
	if cmd == nil {
		t.Fatalf("1 on a plan read to its end sent nothing: %q", lastNote(m))
	}
	cmd()
	if got := r.replies(); len(got) != 1 || got[0]["plan_sha"] != it.PlanSHA {
		t.Fatalf("the approve sent %v", got)
	}

	// Keep planning needs no reading.
	it2, text2 := planItem(30)
	it2.RequestID, it2.PlanSHA = "p6", "other"
	m2, r2 := approvalsOS(t, it2)
	r2.plan, r2.sha = text2, it2.PlanSHA
	fetchPlan(t, m2)
	drawn(m2)
	settleShown(m2)
	press(m2, "3")
	if cmd := m2.InboxNumber(3); cmd == nil {
		t.Fatal("3 on an unread plan did not keep it planning")
	}
}

// TestPlanOfAnotherDigestIsNotShown: a plan read back with another digest
// than the item's is not shown, so a plan can never be approved from text
// other than the one the item names.
func TestPlanOfAnotherDigestIsNotShown(t *testing.T) {
	it, text := planItem(3)
	m, r := approvalsOS(t, it)
	r.plan, r.sha = text, "not-the-item's"
	fetchPlan(t, m)
	plain := drawn(m)
	if strings.Contains(plain, "step 1") || !strings.Contains(plain, "The plan changed") {
		t.Fatalf("a plan of another digest is shown:\n%s", plain)
	}
	settleShown(m)
	press(m, "1")
	if cmd := m.InboxNumber(1); cmd != nil {
		t.Fatal("a plan of another digest was approved")
	}
}

// TestRiskyPeekAllowsOnTheSecondPress: the peek's approve on a risky prompt
// needs the same key twice, and sends the rules with it.
func TestRiskyPeekAllowsOnTheSecondPress(t *testing.T) {
	var sent []map[string]any
	f := &fakeDaemon{peeks: []session.PromptPeek{claudePeek("p1")}, respond: func(p map[string]any) (json.RawMessage, error) {
		sent = append(sent, p)
		return json.Marshal(session.PromptResponse{Sent: "1", SettledBy: "state", State: "working"})
	}}
	m := peekOS(t, f)
	m.Inbox.Items[0].Risk = []string{risk.RuleRecursiveDelete}
	run(t, m, m.InboxPeek())

	press(m, "a")
	if cmd := m.InboxAnswer(harness.ActionApprove, ""); cmd != nil {
		t.Fatal("ASSERTION: the first a on a risky prompt answered it")
	}
	if until := m.Inbox.approvals.armed.at.Add(inboxRiskPressWindow).Format("15:04:05"); !strings.Contains(m.Inbox.Peek.Note, "Press a again by "+until+" to allow") {
		t.Errorf("note %q does not say until when the second press is taken", m.Inbox.Peek.Note)
	}
	press(m, "a")
	run(t, m, m.InboxAnswer(harness.ActionApprove, ""))
	if len(sent) != 1 || fmt.Sprint(sent[0]["risk_ack"]) != "[recursive delete]" {
		t.Fatalf("respond sent %v", sent)
	}

	// d denies at once.
	f.peeks = []session.PromptPeek{claudePeek("p2")}
	run(t, m, m.InboxPeek())
	press(m, "d")
	run(t, m, m.InboxAnswer(harness.ActionDeny, ""))
	if len(sent) != 2 || sent[1]["risk_ack"] != nil {
		t.Fatalf("the deny sent %v", sent)
	}
}

// TestPlanOnAShortScreenIsNotReadToTheEnd: on a screen too short for the
// panel, the panel is squeezed past its minimum and the bottom of the detail
// is cut off, so drawing the window with the plan's last line does not count
// as reading it. 1 stays refused, the detail says to answer in the pane, and
// at a height that fits the same plan is read and approved.
//
// Negative control: with the fit check removed from inboxPlanDetail, the
// first 1 on the short screen sends the approve.
func TestPlanOnAShortScreenIsNotReadToTheEnd(t *testing.T) {
	it, text := planItem(3)
	m, r := approvalsOS(t, it)
	m.Height = 12
	r.plan, r.sha = text, it.PlanSHA
	fetchPlan(t, m)
	plain := drawn(m)
	if !strings.Contains(plain, "too short to show the whole plan") {
		t.Errorf("the short screen does not say the plan cannot be shown whole:\n%s", plain)
	}
	settleShown(m)
	press(m, "1")
	if cmd := m.InboxNumber(1); cmd != nil {
		t.Fatal("ASSERTION: 1 approved a plan drawn on a screen too short to show it")
	}
	if m.inboxPlanReadToEnd(it) {
		t.Error("the plan is recorded as read to its end on a screen too short to show it")
	}

	m.Height = 24
	if plain := drawn(m); strings.Contains(plain, "too short") {
		t.Errorf("a screen that fits still says it is too short:\n%s", plain)
	}
	settleShown(m)
	press(m, "1")
	cmd := m.InboxNumber(1)
	if cmd == nil {
		t.Fatalf("1 on a plan read to its end sent nothing: %q", lastNote(m))
	}
	cmd()
	if got := r.replies(); len(got) != 1 || got[0]["plan_sha"] != it.PlanSHA {
		t.Fatalf("the approve sent %v", got)
	}
}
