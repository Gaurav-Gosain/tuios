package app

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// These tests pin what E2E cannot reach in the Inbox's lifecycle: the keys
// against an older daemon, the mark-attention probe, the fold's zero cost
// without agents, and an unread mark that must not swallow a later alert.

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

func lastNotice(m *OS) Notification {
	if len(m.Notifications) == 0 {
		return Notification{}
	}
	return m.Notifications[len(m.Notifications)-1]
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

// TestProbeVerb: the probe reads list-verbs. A listing with the verb
// is supported, unknown_verb is an older daemon, and a failure that says
// nothing about the verb leaves it unknown.
func TestProbeVerb(t *testing.T) {
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
	if s, k := probeVerb(listing("mark-attention"), "mark-attention"); !s || !k {
		t.Errorf("a listing with the verb read as supported=%v known=%v", s, k)
	}
	if s, k := probeVerb(listing(), "mark-attention"); s || !k {
		t.Errorf("a listing without the verb read as supported=%v known=%v", s, k)
	}
	unknown := func(string, map[string]any) ([]byte, error) {
		return nil, &session.VerbCallError{Code: session.ErrVerbUnknownVerb}
	}
	if s, k := probeVerb(unknown, "mark-attention"); s || !k {
		t.Errorf("unknown_verb read as supported=%v known=%v", s, k)
	}
	broken := func(string, map[string]any) ([]byte, error) { return nil, errors.New("connection reset") }
	if _, k := probeVerb(broken, "mark-attention"); k {
		t.Error("a broken connection read as an answer")
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
