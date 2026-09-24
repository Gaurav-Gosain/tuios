package app

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/charmbracelet/x/ansi"
)

// These tests pin the client half of the Inbox's lifecycle: the snoozed
// mirror, the snooze picker, undo, the ctrl+b O walk, the rail's u and z, and
// the fold of rows at rest.

// markRecorder answers mark-attention and records what it was sent.
type markRecorder struct {
	calls []map[string]any
	err   error
}

func (r *markRecorder) call(verb string, params map[string]any, _ time.Duration) (json.RawMessage, error) {
	if verb != "mark-attention" {
		return nil, &session.VerbCallError{Code: session.ErrVerbUnknownVerb, Message: verb}
	}
	r.calls = append(r.calls, params)
	if r.err != nil {
		return nil, r.err
	}
	id, _ := params["id"].(string)
	if id == "" {
		id = "90"
	}
	return json.Marshal(map[string]any{"type": "attention_marked", "id": id, "action": params["action"]})
}

// lifecycleOS is a client with an Inbox and a recorder behind mark-attention.
func lifecycleOS(t *testing.T, items ...session.AttentionItem) (*OS, *markRecorder) {
	t.Helper()
	m := inboxOS(t, zeroSettle())
	r := &markRecorder{}
	m.SetInboxVerbCaller(r.call, func() string { return "nonce-1" })
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: items})
	return m, r
}

// runMarked runs a command and feeds an InboxMarkedMsg back.
func runMarked(t *testing.T, m *OS, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command to run")
	}
	msg, ok := cmd().(InboxMarkedMsg)
	if !ok {
		t.Fatal("the command did not answer with InboxMarkedMsg")
	}
	m.applyInboxMarked(msg)
}

func lastNotice(m *OS) Notification {
	if len(m.Notifications) == 0 {
		return Notification{}
	}
	return m.Notifications[len(m.Notifications)-1]
}

// TestTheSnoozedMirrorFollowsEvents: a listing's snoozed items go to their own
// list, a snooze close moves an item there, an open takes it back, and a close
// of a snoozed item drops it.
func TestTheSnoozedMirrorFollowsEvents(t *testing.T) {
	asleep := item("3", session.AttentionErrored, "a", "w3", "", 5)
	asleep.SnoozedUntil = -1
	m, _ := lifecycleOS(t,
		item("1", session.AttentionFinished, "a", "w1", "", 10),
		item("2", session.AttentionErrored, "a", "w2", "", 20),
		asleep,
	)
	if len(m.Inbox.Items) != 2 || len(m.Inbox.life.Snoozed) != 1 {
		t.Fatalf("the listing split into %d open and %d snoozed", len(m.Inbox.Items), len(m.Inbox.life.Snoozed))
	}
	closed := item("2", session.AttentionErrored, "a", "w2", "", 20)
	closed.Closed, closed.SnoozedUntil = session.AttentionClosedSnoozed, 99
	m.applyInboxEvents(InboxEventsMsg{Events: []InboxEvent{{Action: session.AttentionClosed, Item: &closed}}})
	if len(m.Inbox.Items) != 1 || len(m.Inbox.life.Snoozed) != 2 || m.Inbox.life.Snoozed[1].Closed != "" {
		t.Fatalf("a snooze left %d open and %+v snoozed", len(m.Inbox.Items), m.Inbox.life.Snoozed)
	}
	woke := item("3", session.AttentionErrored, "a", "w3", "", 5)
	m.applyInboxEvents(opened(woke))
	if len(m.Inbox.Items) != 2 || len(m.Inbox.life.Snoozed) != 1 {
		t.Fatalf("a wake left %d open and %d snoozed", len(m.Inbox.Items), len(m.Inbox.life.Snoozed))
	}
	gone := item("2", session.AttentionErrored, "a", "w2", "", 20)
	gone.Closed = session.AttentionClosedResolved
	m.applyInboxEvents(InboxEventsMsg{Events: []InboxEvent{{Action: session.AttentionClosed, Item: &gone}}})
	if len(m.Inbox.life.Snoozed) != 0 || len(m.Inbox.Items) != 2 {
		t.Fatalf("a close of a snoozed item left %d snoozed, %d open", len(m.Inbox.life.Snoozed), len(m.Inbox.Items))
	}
}

// TestWhatThePersonAskedForDoesNotAlert: an item that opens because this
// client woke or restored it, or marked its pane unread, raises no alert; the
// same item opening on its own does.
func TestWhatThePersonAskedForDoesNotAlert(t *testing.T) {
	m, _ := lifecycleOS(t)
	m.inboxMarkCmd(map[string]any{"id": "4", "action": "restore"}, "api", "")
	m.inboxMarkCmd(map[string]any{"session": "here", "window": "w-2", "action": "unread"}, "docs", "")
	if m.inboxAskedFor(item("9", session.AttentionErrored, "here", "w-1", "", 1)) {
		t.Error("an item nobody asked for reads as asked for")
	}
	if !m.inboxAskedFor(item("4", session.AttentionErrored, "here", "w-1", "", 1)) {
		t.Error("the restored item alerts")
	}
	if !m.inboxAskedFor(item("12", session.AttentionFinished, "here", "w-2", "", 1)) {
		t.Error("the item unread opened alerts")
	}
	if m.inboxAskedFor(item("4", session.AttentionErrored, "here", "w-1", "", 1)) {
		t.Error("the same item opening again later is still quiet")
	}

	// Through the event path: the restored item's open schedules no alert,
	// an item nobody asked for does.
	// The fixture fires alerts at once, into the dock.
	m.inboxMarkCmd(map[string]any{"id": "5", "action": "restore"}, "api", "")
	m.Notifications = nil
	m.applyInboxEvents(opened(item("5", session.AttentionErrored, "other", "w-9", "x", 1)))
	if len(m.Notifications) != 0 {
		t.Errorf("the restored item's open alerts: %q", lastNotice(m).Message)
	}
	m.applyInboxEvents(opened(item("6", session.AttentionErrored, "other", "w-8", "y", 1)))
	if len(m.Notifications) == 0 {
		t.Error("an item nobody asked for raises no alert")
	}
}

// TestSnoozeOffersFourLengths: z opens the picker with the four lengths in the
// footer, a digit sends that length with the nonce, and the dock says u undoes
// it for five seconds. Any other key closes the picker.
func TestSnoozeOffersFourLengths(t *testing.T) {
	m, r := lifecycleOS(t, item("1", session.AttentionFinished, "here", "w-1", "done", 10))
	m.OpenInbox("")
	if _, handled := m.InboxSnooze(); !handled || !m.InboxSnoozePicking() {
		t.Fatal("z did not open the picker")
	}
	out, _, _ := m.renderInbox()
	plain := ansi.Strip(out)
	for _, want := range []string{"1 15m", "2 1h", "3 tomorrow 9:00", "4 until it changes", "esc cancel"} {
		if !strings.Contains(plain, want) {
			t.Errorf("the picker's footer lacks %q:\n%s", want, plain)
		}
	}
	if cmd := m.InboxSnoozeKey("esc"); cmd != nil || m.InboxSnoozePicking() || len(r.calls) != 0 {
		t.Fatal("esc snoozed, or left the picker open")
	}

	cases := []struct {
		key   string
		check func(p map[string]any) bool
	}{
		{"1", func(p map[string]any) bool { return p["for_ms"] == (15 * time.Minute).Milliseconds() }},
		{"2", func(p map[string]any) bool { return p["for_ms"] == time.Hour.Milliseconds() }},
		{"3", func(p map[string]any) bool {
			ms, _ := p["until"].(int64)
			at := time.UnixMilli(ms)
			return at.Hour() == 9 && at.Minute() == 0 && at.After(time.Now()) && at.Sub(time.Now()) <= 33*time.Hour
		}},
		{"4", func(p map[string]any) bool { return p["until_change"] == true }},
	}
	for _, c := range cases {
		m.Notifications = nil
		m.InboxSnooze()
		runMarked(t, m, m.InboxSnoozeKey(c.key))
		p := r.calls[len(r.calls)-1]
		if p["action"] != "snooze" || p["id"] != "1" || p["human_nonce"] != "nonce-1" || !c.check(p) {
			t.Errorf("%s sent %v", c.key, p)
		}
		n := lastNotice(m)
		if !strings.Contains(n.Message, "Snoozed agent-1") || !strings.Contains(n.Message, "u in the Inbox undoes") || n.Duration < inboxUndoNotice {
			t.Errorf("%s said %q for %v", c.key, n.Message, n.Duration)
		}
	}
}

// TestSnoozeIsNotOfferedOnWhatWaitsForAnAnswer: a held approval, a plan and a
// question put with ask-human say why and open no picker, and the footer does
// not offer z on them.
func TestSnoozeIsNotOfferedOnWhatWaitsForAnAnswer(t *testing.T) {
	held := item("1", session.AttentionApproval, "here", "w-1", "Bash: ls", 10)
	held.RequestID, held.Options = "r1", []string{session.ApprovalOnce, session.ApprovalDeny}
	for _, it := range []session.AttentionItem{held, item("2", session.AttentionPlan, "here", "w-2", "plan", 10)} {
		m, r := lifecycleOS(t, it)
		m.OpenInbox("")
		m.InboxSnooze()
		if m.InboxSnoozePicking() || len(r.calls) != 0 {
			t.Errorf("%s opened the picker", it.Kind)
		}
		if n := lastNotice(m); !strings.Contains(n.Message, "not snoozed") {
			t.Errorf("%s said %q", it.Kind, n.Message)
		}
		for _, h := range m.inboxRowHints(it, true) {
			if h.Label == "snooze" {
				t.Errorf("%s offers snooze in its footer", it.Kind)
			}
		}
	}
}

// TestUndoRestoresTheLastClose: a dismiss that went through says u undoes it,
// u sends restore for it, a second u restores the one before, and after ten
// seconds there is nothing to undo.
func TestUndoRestoresTheLastClose(t *testing.T) {
	m, r := lifecycleOS(t)
	m.applyInboxDismissed(InboxDismissedMsg{ID: "4", Kind: session.AttentionErrored, Who: "api"})
	if n := lastNotice(m); n.Message != "Dismissed api. u undoes." || n.Duration < inboxUndoNotice {
		t.Errorf("the dismiss said %q for %v", n.Message, n.Duration)
	}
	m.applyInboxDismissed(InboxDismissedMsg{ID: "5", Kind: session.AttentionFinished, Who: "docs"})
	// A silent dismiss (a turn seen under the person's eyes) is not undoable.
	m.applyInboxDismissed(InboxDismissedMsg{ID: "6", Kind: session.AttentionFinished, Who: "web", Silent: true})

	cmd, handled := m.InboxUndo()
	if !handled {
		t.Fatal("u did nothing")
	}
	runMarked(t, m, cmd)
	if p := r.calls[0]; p["action"] != "restore" || p["id"] != "5" {
		t.Fatalf("u sent %v, want restore of 5", p)
	}
	if n := lastNotice(m); n.Message != "Restored docs" {
		t.Errorf("the restore said %q", n.Message)
	}
	cmd, _ = m.InboxUndo()
	runMarked(t, m, cmd)
	if p := r.calls[1]; p["id"] != "4" {
		t.Fatalf("the second u restored %v, want 4", p["id"])
	}

	m.applyInboxDismissed(InboxDismissedMsg{ID: "7", Kind: session.AttentionErrored, Who: "old"})
	m.Inbox.life.undo[0].at = time.Now().Add(-inboxUndoWindow)
	if cmd, handled := m.InboxUndo(); cmd != nil || !handled {
		t.Error("u restored a close older than ten seconds")
	}
	if n := lastNotice(m); !strings.Contains(n.Message, "Nothing to undo") {
		t.Errorf("an expired undo said %q", n.Message)
	}

	// Mail discarded for another machine and an answered ask cannot come back.
	m.applyInboxDismissed(InboxDismissedMsg{ID: "8", Kind: session.AttentionOutbox, Who: "build"})
	if len(m.Inbox.life.undo) != 0 {
		t.Error("an outbox dismiss was kept for undo")
	}
}

// TestSnoozedItemsShowOnS: the list says how many are snoozed and the key that
// shows them; S lists them under a Snoozed heading, muted, with when each
// wakes, and z on one wakes it.
func TestSnoozedItemsShowOnS(t *testing.T) {
	asleep := item("3", session.AttentionErrored, "here", "w-2", "broke", 5)
	asleep.SnoozedUntil = session.SnoozeUntilChange
	m, r := lifecycleOS(t, item("1", session.AttentionFinished, "here", "w-1", "done", 10), asleep)
	m.OpenInbox("")
	plain := ansi.Strip(func() string { out, _, _ := m.renderInbox(); return out }())
	if !strings.Contains(plain, "1 snoozed. S shows it.") || strings.Contains(plain, "broke") {
		t.Fatalf("with the snoozed hidden the Inbox reads:\n%s", plain)
	}
	if _, handled := m.InboxToggleSnoozed(); !handled {
		t.Fatal("S did nothing")
	}
	plain = ansi.Strip(func() string { out, _, _ := m.renderInbox(); return out }())
	if !strings.Contains(plain, "Snoozed 1") || !strings.Contains(plain, "until it changes") || !strings.Contains(plain, "broke") {
		t.Fatalf("with the snoozed shown the Inbox reads:\n%s", plain)
	}
	m.InboxMove(1 << 20)
	sel, _ := m.inboxSelected()
	if sel.ID != "3" {
		t.Fatalf("the cursor cannot reach the snoozed item, it is on %q", sel.ID)
	}
	cmd, _ := m.InboxSnooze()
	runMarked(t, m, cmd)
	if p := r.calls[0]; p["action"] != "wake" || p["id"] != "3" {
		t.Errorf("z on a snoozed item sent %v", p)
	}

	// Everything snoozed: the empty state says so.
	m2, _ := lifecycleOS(t, asleep)
	m2.OpenInbox("")
	plain = ansi.Strip(func() string { out, _, _ := m2.renderInbox(); return out }())
	if !strings.Contains(plain, "Nothing is waiting for you.") || !strings.Contains(plain, "1 snoozed") {
		t.Errorf("an Inbox with only snoozed items reads:\n%s", plain)
	}
}

// TestNextFinishedWalksNewestFirst: ctrl+b O goes to the newest finished turn,
// a repeat steps to the next older one, and a turn that finishes during the
// walk starts it over. With no agent ever seen the key is not handled, so it
// reaches the pane.
func TestNextFinishedWalksNewestFirst(t *testing.T) {
	m, _ := lifecycleOS(t)
	if _, handled := m.JumpToNewestFinished(); handled {
		t.Fatal("ctrl+b O was handled with no agent ever seen")
	}
	older := item("1", session.AttentionFinished, "here", "w-1", "", 10)
	older.Seq = 5
	newer := item("2", session.AttentionFinished, "here", "w-2", "", 20)
	newer.Seq = 9
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{older, newer}})
	m.SidebarAgentsSeen = true

	focused := func() string {
		if w := m.GetFocusedWindow(); w != nil {
			return w.ID
		}
		return ""
	}
	if _, handled := m.JumpToNewestFinished(); !handled || focused() != "w-2" {
		t.Fatalf("the first press went to %q, want the newest, w-2", focused())
	}
	if _, _ = m.JumpToNewestFinished(); focused() != "w-1" {
		t.Fatalf("the repeat went to %q, want the older, w-1", focused())
	}
	// A new turn finishes: the walk starts over at it.
	fresh := item("2", session.AttentionFinished, "here", "w-2", "", 20)
	fresh.Seq = 12
	m.applyInboxEvents(InboxEventsMsg{Events: []InboxEvent{{Action: session.AttentionUpdated, Item: &fresh}}})
	if _, _ = m.JumpToNewestFinished(); focused() != "w-2" {
		t.Fatalf("after a new turn the walk went to %q, want w-2", focused())
	}
	// Past the repeat window it starts over too.
	m.Inbox.life.walk.at = time.Now().Add(-inboxCycleWindow)
	m.FocusWindow(0)
	if _, _ = m.JumpToNewestFinished(); focused() != "w-2" {
		t.Errorf("after the window the walk went to %q, want w-2", focused())
	}
}

// TestRailUnreadForgetsTheLook: u on a rail agent row clears this client's
// seen marks for the pane and asks the daemon to open its Finished item
// unread. The focused pane is refused.
func TestRailUnreadForgetsTheLook(t *testing.T) {
	m, r := lifecycleOS(t)
	m.Windows[1].AgentState = "idle"
	m.Windows[1].AgentCompletionSeq = 3
	m.SidebarAgentSeen = map[string]bool{"w-2": true}
	m.SidebarAgentSeenSeq = map[string]uint64{"w-2": 3}
	m.FocusWindow(0)

	cmd, handled := m.SidebarAgentUnread("here", "w-2")
	if !handled {
		t.Fatal("u did nothing")
	}
	if m.SidebarAgentSeen["w-2"] || m.SidebarAgentSeenSeq["w-2"] != 0 {
		t.Error("the seen marks were not cleared")
	}
	if !m.agentFinishedUnread("w-2", "idle", 3) {
		t.Error("the rail does not read the pane as finished and unread")
	}
	runMarked(t, m, cmd)
	if p := r.calls[0]; p["action"] != "unread" || p["session"] != "here" || p["window"] != "w-2" || p["human_nonce"] != "nonce-1" {
		t.Errorf("u sent %v", p)
	}

	// The pane in front of the person reads as seen, so it is refused.
	m.Windows[0].AgentState, m.Windows[0].AgentCompletionSeq = "idle", 1
	m.SidebarAgentSeenSeq["w-1"] = 1
	if cmd, _ := m.SidebarAgentUnread("here", "w-1"); cmd != nil || m.SidebarAgentSeenSeq["w-1"] != 1 {
		t.Error("u marked the focused pane unread")
	}
	// A pane that never finished has nothing to mark.
	m.Windows[1].AgentCompletionSeq, m.Windows[1].AgentState = 0, "working"
	if cmd, _ := m.SidebarAgentUnread("here", "w-2"); cmd != nil {
		t.Error("u marked a pane that has not finished a turn")
	}
}

// TestRailSnoozeOpensThePicker: z on a rail agent row opens the Inbox on the
// pane's item with the picker, and picking closes the Inbox again.
func TestRailSnoozeOpensThePicker(t *testing.T) {
	m, r := lifecycleOS(t,
		item("1", session.AttentionFinished, "here", "w-1", "", 10),
		item("2", session.AttentionErrored, "here", "w-2", "", 20),
	)
	if _, handled := m.SidebarAgentSnooze("here", "w-2"); !handled || !m.ShowInbox || !m.InboxSnoozePicking() {
		t.Fatal("z did not open the Inbox with the picker")
	}
	if sel, _ := m.inboxSelected(); sel.ID != "2" {
		t.Errorf("the Inbox opened on %q, want the pane's item", sel.ID)
	}
	runMarked(t, m, m.InboxSnoozeKey("2"))
	if m.ShowInbox || r.calls[0]["id"] != "2" {
		t.Errorf("after the pick the Inbox is open %v and the call was %v", m.ShowInbox, r.calls[0])
	}
	if _, handled := m.SidebarAgentSnooze("here", "w-9"); !handled || m.ShowInbox {
		t.Error("z on a pane with no item opened the Inbox")
	}
}

// TestRowsLongAtRestFold: rows at rest past the threshold fold into one line
// at the end, a row that needs the person, a finished turn not yet seen or a
// working agent never does, the fold opens on request until the rail lets go
// of the keyboard, and a threshold of zero never folds.
func TestRowsLongAtRestFold(t *testing.T) {
	now := time.Now()
	old := now.Add(-2 * time.Hour).UnixNano()
	agents := []sidebarAgentEntry{
		{SessionID: "s", WindowID: "ask", Title: "ask", State: "needs_input", StateAt: old},
		{SessionID: "s", WindowID: "new", Title: "new", State: "done", StateAt: old},
		{SessionID: "s", WindowID: "run", Title: "run", State: "working", StateAt: old},
		{SessionID: "s", WindowID: "a", Title: "docs", State: "idle", StateAt: old},
		{SessionID: "s", WindowID: "b", Title: "web", State: "done", DoneSeen: true, StateAt: old},
		{SessionID: "s", WindowID: "c", Title: "api", State: "unknown", StateAt: old},
		{SessionID: "s", WindowID: "d", Title: "fresh", State: "idle", StateAt: now.Add(-time.Minute).UnixNano()},
		{SessionID: "s", WindowID: "e", Title: "here", State: "idle", StateAt: old, Focused: true},
	}
	m := &OS{Settings: config.Global}
	m.Settings.SidebarAgentRestFold = time.Hour
	got := m.sidebarFoldAgents(agents, now)
	if len(got) != 6 {
		t.Fatalf("folded to %d rows, want 5 and the fold", len(got))
	}
	fold := got[len(got)-1]
	if fold.Fold != 3 || fold.FoldNames != "docs, web, api" {
		t.Fatalf("the fold is %+v, want the three rows at rest", fold)
	}
	for _, e := range got[:len(got)-1] {
		switch e.WindowID {
		case "ask", "new", "run", "d", "e":
		default:
			t.Errorf("%s folded", e.WindowID)
		}
	}
	m.SidebarUnfoldAgents()
	if got := m.sidebarFoldAgents(agents, now); len(got) != len(agents) {
		t.Error("unfolding left the rows folded")
	}
	m.SidebarFocused = true
	m.ExitSidebarFocus()
	if !(len(m.sidebarFoldAgents(agents, now)) == 6) {
		t.Error("the fold did not close again when the rail let go of the keyboard")
	}
	m.Settings.SidebarAgentRestFold = 0
	if got := m.sidebarFoldAgents(agents, now); len(got) != len(agents) {
		t.Error("a threshold of zero folded")
	}
	// One row at rest folds nothing: one line for one line saves nothing.
	m.Settings.SidebarAgentRestFold = time.Hour
	if got := m.sidebarFoldAgents(agents[3:4], now); len(got) != 1 || got[0].Fold != 0 {
		t.Error("a single row at rest folded")
	}
}

// TestTheFoldDrawsAndOpens renders a rail with rows at rest: the fold line
// is drawn and hit-tested, enter on it opens it, and the rail's signature
// folds the minute only once an agent has been seen.
func TestTheFoldDrawsAndOpens(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.Settings.SidebarAgentRestFold = time.Hour
	old := time.Now().Add(-3 * time.Hour).UnixNano()
	for i := range tree.Sessions {
		for j := range tree.Sessions[i].Children {
			c := &tree.Sessions[i].Children[j]
			if c.AgentState != "" {
				c.AgentState, c.StateAt, c.DoneSeen = "idle", old, false
			}
		}
	}
	lines := railPlain(t, m, tree)
	if lineOf(lines, "+3 at rest") < 0 {
		t.Fatalf("no fold line:\n%s", strings.Join(lines, "\n"))
	}
	found := false
	for i, r := range m.SidebarNav {
		if r.Kind == sidebarRowAgentFold {
			found = true
			m.SidebarFocused = true
			m.sidebarSetCursor(i)
			m.SidebarActivateCursor()
		}
	}
	if !found || !m.sidebarAgentsUnfolded {
		t.Fatal("the fold row is not on the cursor's path, or enter did not open it")
	}
	lines = railPlain(t, m, tree)
	if lineOf(lines, "at rest") >= 0 || lineOf(lines, "server") < 0 {
		t.Errorf("the opened fold still reads:\n%s", strings.Join(lines, "\n"))
	}
}

// TestNoFoldClockWithoutAgents: a rail that has never seen an agent keys no
// clock into its signature, so it is never rebuilt for the fold; once one has
// been seen, the minute moves the signature.
func TestNoFoldClockWithoutAgents(t *testing.T) {
	withSidebar(t, true, "right", config.SidebarDefaultWidth)
	m := newTestOS(&terminal.Window{ID: "w-1", Width: 40, Height: 20, Workspace: 1})
	m.Settings = config.Global
	m.Settings.SidebarAgentRestFold = time.Hour
	t0 := time.Unix(1_800_000_000, 0)
	clock := t0
	sidebarFoldClock = func() time.Time { return clock }
	t.Cleanup(func() { sidebarFoldClock = time.Now })

	a := m.sidebarSignature()
	clock = t0.Add(2 * time.Minute)
	if m.sidebarSignature() != a {
		t.Error("with no agent seen the minute changed the rail's signature")
	}
	m.SidebarAgentsSeen = true
	b := m.sidebarSignature()
	clock = t0.Add(4 * time.Minute)
	if m.sidebarSignature() == b {
		t.Error("with an agent seen the minute does not move the signature, so a row never folds")
	}
}

// TestOlderDaemonHidesTheLifecycleKeys: against a daemon whose list-verbs has
// no mark-attention, the footer offers no snooze, z, u and S do what an
// unbound key does, a dismiss that went through says nothing and keeps no
// undo, rail z falls through to the rail, and rail u clears only this
// client's marks.
func TestOlderDaemonHidesTheLifecycleKeys(t *testing.T) {
	m, r := lifecycleOS(t)
	m.applyInboxSnapshot(InboxSnapshotMsg{
		Items:  []session.AttentionItem{item("1", session.AttentionFinished, "here", "w-1", "done", 10)},
		NoMark: true,
	})
	m.OpenInbox("")
	it, _ := m.inboxSelected()
	for _, h := range m.inboxRowHints(it, true) {
		if h.Label == "snooze" || h.Label == "wake" {
			t.Errorf("the footer offers %q on a daemon without mark-attention", h.Label)
		}
	}
	for name, act := range map[string]func() (tea.Cmd, bool){
		"z": m.InboxSnooze, "u": m.InboxUndo, "S": m.InboxToggleSnoozed,
	} {
		if cmd, handled := act(); cmd != nil || handled {
			t.Errorf("%s was handled on a daemon without mark-attention", name)
		}
	}
	m.Notifications = nil
	m.applyInboxDismissed(InboxDismissedMsg{ID: "4", Kind: session.AttentionErrored, Who: "api"})
	if len(m.Notifications) != 0 || len(m.Inbox.life.undo) != 0 {
		t.Errorf("a dismiss promised an undo: %q, %d kept", lastNotice(m).Message, len(m.Inbox.life.undo))
	}
	if _, handled := m.SidebarAgentSnooze("here", "w-1"); handled {
		t.Error("rail z was handled on a daemon without mark-attention")
	}

	m.Windows[1].AgentState, m.Windows[1].AgentCompletionSeq = "idle", 3
	m.SidebarAgentSeenSeq = map[string]uint64{"w-2": 3}
	m.FocusWindow(0)
	cmd, handled := m.SidebarAgentUnread("here", "w-2")
	if !handled || cmd != nil || len(r.calls) != 0 {
		t.Errorf("rail u sent mark-attention to a daemon without it (cmd %v, calls %v)", cmd != nil, r.calls)
	}
	if m.SidebarAgentSeenSeq["w-2"] != 0 {
		t.Error("rail u did not clear this client's marks")
	}

	// A newer daemon on the next watch brings the keys back.
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{item("1", session.AttentionFinished, "here", "w-1", "done", 10)}})
	if !m.inboxMarkSupported() {
		t.Error("a listing from a daemon with mark-attention left the keys hidden")
	}
}

// TestAnUnknownMarkHidesTheKeys: a mark that comes back unknown_verb is the
// same finding as the probe's, so the keys go too.
func TestAnUnknownMarkHidesTheKeys(t *testing.T) {
	m, _ := lifecycleOS(t)
	m.applyInboxMarked(InboxMarkedMsg{Action: "snooze", Err: &session.VerbCallError{Code: session.ErrVerbUnknownVerb, Message: "unknown verb"}})
	if m.inboxMarkSupported() {
		t.Error("an unknown_verb answer left mark-attention looking supported")
	}
}

// TestProbeMarkAttention: the probe reads list-verbs. A listing with the verb
// is supported, unknown_verb is an older daemon, and a failure that says
// nothing about the verb leaves it unknown.
func TestProbeMarkAttention(t *testing.T) {
	listing := func(verbs ...string) func(string, map[string]any) ([]byte, error) {
		return func(verb string, params map[string]any) ([]byte, error) {
			if verb != "list-verbs" || params["verb"] != "mark-attention" {
				t.Fatalf("probe called %s %v", verb, params)
			}
			var docs []map[string]string
			for _, v := range verbs {
				docs = append(docs, map[string]string{"verb": v})
			}
			return json.Marshal(map[string]any{"verbs": docs})
		}
	}
	if s, k := probeMarkAttention(listing("mark-attention")); !s || !k {
		t.Errorf("a listing with the verb read as supported=%v known=%v", s, k)
	}
	if s, k := probeMarkAttention(listing()); s || !k {
		t.Errorf("a listing without the verb read as supported=%v known=%v", s, k)
	}
	unknown := func(string, map[string]any) ([]byte, error) {
		return nil, &session.VerbCallError{Code: session.ErrVerbUnknownVerb}
	}
	if s, k := probeMarkAttention(unknown); s || !k {
		t.Errorf("unknown_verb read as supported=%v known=%v", s, k)
	}
	broken := func(string, map[string]any) ([]byte, error) { return nil, errors.New("connection reset") }
	if _, k := probeMarkAttention(broken); k {
		t.Error("a broken connection read as an answer")
	}
}

// TestAClickUnfoldEndsWhenAPaneIsFocused: a click can open the fold while the
// rail does not have the keyboard, so there is no letting go of it to wait
// for. The rows fold again once a pane is focused. An unfold with the
// keyboard on the rail lasts until the rail lets go.
func TestAClickUnfoldEndsWhenAPaneIsFocused(t *testing.T) {
	m, _ := attentionOS(t, 120, 40)
	m.SidebarFocused = false
	m.SidebarUnfoldAgents()
	m.FocusWindow(1)
	if m.sidebarAgentsUnfolded {
		t.Error("the rows a click unfolded stayed open after a pane was focused")
	}

	// A click outside the rail, on the pane already focused, folds them too.
	m.SidebarUnfoldAgents()
	m.RefoldAgentsAfterClickAway()
	if m.sidebarAgentsUnfolded {
		t.Error("the rows a click unfolded stayed open after a click outside the rail")
	}

	m.SidebarFocused = true
	m.SidebarUnfoldAgents()
	m.RefoldAgentsAfterClickAway()
	m.FocusWindow(0)
	if !m.sidebarAgentsUnfolded {
		t.Error("focusing a pane from the rail folded the rows while the rail has the keyboard")
	}
	m.ExitSidebarFocus()
	if m.sidebarAgentsUnfolded {
		t.Error("the rows stayed open after the rail let go of the keyboard")
	}
}

// TestAnUnreadMarkedInPlaceSilencesNothingLater: unread on a pane whose
// finished item is already open marks it in place, with an update that
// consumes no ask. The answer settles the ask, so a new item on that pane
// in the next seconds still alerts, and an approval is never taken for the
// unread's item.
func TestAnUnreadMarkedInPlaceSilencesNothingLater(t *testing.T) {
	fin := item("7", session.AttentionFinished, "other", "w-2", "done", 10)
	m, _ := lifecycleOS(t, fin)
	cmd := m.inboxMarkCmd(map[string]any{"session": "other", "window": "w-2", "action": "unread"}, "docs", "")
	marked := fin
	marked.MarkedUnread = true
	m.applyInboxEvents(InboxEventsMsg{Events: []InboxEvent{{Action: session.AttentionUpdated, Item: &marked}}})
	msg := cmd().(InboxMarkedMsg)
	msg.ID = "7"
	m.applyInboxMarked(msg)

	m.Notifications = nil
	m.applyInboxEvents(opened(item("8", session.AttentionErrored, "other", "w-2", "tests failed", 10)))
	if len(m.Notifications) == 0 {
		t.Error("an error on the pane just marked unread raised no alert")
	}
	if m.inboxAskedFor(item("9", session.AttentionFinished, "other", "w-2", "", 1)) {
		t.Error("the pane's key outlived the unread's answer")
	}

	// Before the answer, an approval on the pane is still not the unread's.
	m.inboxMarkCmd(map[string]any{"session": "other", "window": "w-2", "action": "unread"}, "docs", "")
	if m.inboxAskedFor(item("10", session.AttentionApproval, "other", "w-2", "", 1)) {
		t.Error("an approval was taken for the unread's finished item")
	}

	// An answer that arrives before the open keeps the open quiet by id.
	m.settleInboxUnreadAsk("w-3", "11")
	if !m.inboxAskedFor(item("11", session.AttentionFinished, "other", "w-3", "", 1)) {
		t.Error("the item the unread opened after its answer alerts")
	}
}
