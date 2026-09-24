package session

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// These tests pin the Inbox's lifecycle: snooze, wake, unread and restore.

// fakeClock is a clock a test moves by hand.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// lifecycleStore is a recording store on a fake clock.
func lifecycleStore(t *testing.T) (*attentionStore, *[]streamEvent, *fakeClock) {
	t.Helper()
	a, events := recordingAttention()
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	a.now = clock.now
	t.Cleanup(func() {
		a.mu.Lock()
		if a.wakeTimer != nil {
			a.wakeTimer.Stop()
		}
		a.mu.Unlock()
	})
	return a, events, clock
}

func snooze(t *testing.T, a *attentionStore, id string, until int64) *verbError {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	_, verr := a.snoozeLocked(id, until)
	return verr
}

func lastEvent(events []streamEvent) streamEvent { return events[len(events)-1] }

// TestSnoozeClosesAndWakesWithTheSameIdentity: a snooze closes the item with
// the reason snoozed and snoozed_until on the closing copy, and waking opens
// it again with the same id and since.
func TestSnoozeClosesAndWakesWithTheSameIdentity(t *testing.T) {
	a, events, clock := lifecycleStore(t)
	a.noteSessionEvent("work", agentEvent("w1", "working", "errored", "", "boom", 0, 0))
	it := openItems(t, a)[0]

	until := clock.now().Add(time.Hour).UnixNano()
	if verr := snooze(t, a, it.ID, until); verr != nil {
		t.Fatalf("snooze: %v", verr)
	}
	ev := lastEvent(*events)
	if ev.Action != AttentionClosed || ev.Attention.Closed != AttentionClosedSnoozed || ev.Attention.SnoozedUntil != until {
		t.Fatalf("the snooze published %s/%s until %d", ev.Action, ev.Attention.Closed, ev.Attention.SnoozedUntil)
	}
	if items := openItems(t, a); len(items) != 0 {
		t.Fatalf("a snoozed item is still listed: %+v", items)
	}
	listed, counts, _ := a.list(attentionQuery{snoozed: true})
	if len(listed) != 1 || listed[0].SnoozedUntil != until || counts[AttentionErrored] != 0 {
		t.Fatalf("include_snoozed listed %+v with counts %v", listed, counts)
	}

	a.mu.Lock()
	woke, ok := a.wakeLocked(it.ID)
	a.mu.Unlock()
	if !ok || woke.ID != it.ID || woke.Since != it.Since || woke.SnoozedUntil != 0 {
		t.Fatalf("woke %+v, want id %s since %d", woke, it.ID, it.Since)
	}
	if ev := lastEvent(*events); ev.Action != AttentionOpened || ev.Attention.ID != it.ID {
		t.Errorf("the wake published %s for %s", ev.Action, ev.Attention.ID)
	}
}

// TestASnoozedItemWakesWhenItsFactChanges: a report that says something new
// wakes the item with its id; one that repeats what it says does not.
func TestASnoozedItemWakesWhenItsFactChanges(t *testing.T) {
	a, events, _ := lifecycleStore(t)
	a.noteSessionEvent("work", agentEvent("w1", "working", "needs_input", "question", "which branch?", 0, 0))
	it := openItems(t, a)[0]
	if verr := snooze(t, a, it.ID, SnoozeUntilChange); verr != nil {
		t.Fatalf("snooze: %v", verr)
	}
	n := len(*events)
	a.noteSessionEvent("work", agentEvent("w1", "needs_input", "needs_input", "question", "which branch?", 0, 0))
	if len(*events) != n || len(openItems(t, a)) != 0 {
		t.Fatalf("a repeated report woke the item: %s", actions((*events)[n:]))
	}
	a.noteSessionEvent("work", agentEvent("w1", "needs_input", "needs_input", "question", "main or dev?", 0, 0))
	items := openItems(t, a)
	if len(items) != 1 || items[0].ID != it.ID || items[0].Since != it.Since || items[0].Summary != "main or dev?" {
		t.Fatalf("the change left %+v, want %s back with the new question", items, it.ID)
	}
	if ev := lastEvent(*events); ev.Action != AttentionOpened {
		t.Errorf("the wake published %s, want open", ev.Action)
	}

	// A finished turn snoozed until it changes wakes on the next turn, and
	// counts both.
	a.noteSessionEvent("work", agentEvent("w2", "working", "done", "", "one", 0, 1))
	fin := openItems(t, a)[1]
	if verr := snooze(t, a, fin.ID, SnoozeUntilChange); verr != nil {
		t.Fatalf("snooze: %v", verr)
	}
	a.noteSessionEvent("work", agentEvent("w2", "done", "working", "", "", 1, 1))
	listed, _, _ := a.list(attentionQuery{snoozed: true})
	if len(listed) != 1 {
		t.Fatalf("a new turn should drop the snoozed finished item, like the open one: %+v", listed)
	}
	a.noteSessionEvent("work", agentEvent("w2", "working", "done", "", "two", 1, 2))
	if items := openItems(t, a); len(items) != 2 || items[1].Count != 1 {
		t.Errorf("the next turn opened %+v", items)
	}
}

// TestASnoozedItemIsDroppedWhenItsFactEnds: leaving needs_input, a look at
// the pane, or the pane closing drops the sleeping item with that reason.
func TestASnoozedItemIsDroppedWhenItsFactEnds(t *testing.T) {
	a, events, _ := lifecycleStore(t)
	a.noteSessionEvent("work", agentEvent("w1", "working", "needs_input", "approval", "ok?", 0, 0))
	a.noteSessionEvent("work", agentEvent("w2", "working", "done", "", "", 0, 3))
	a.noteSessionEvent("work", agentEvent("w3", "working", "errored", "", "x", 0, 0))
	for _, it := range openItems(t, a) {
		if verr := snooze(t, a, it.ID, SnoozeUntilChange); verr != nil {
			t.Fatalf("snooze %s: %v", it.Kind, verr)
		}
	}
	a.noteSessionEvent("work", agentEvent("w1", "needs_input", "working", "", "", 0, 0))
	if ev := lastEvent(*events); ev.Action != AttentionClosed || ev.Attention.Closed != AttentionClosedResolved || ev.Attention.Window != "w1" {
		t.Errorf("leaving needs_input published %s/%s", ev.Action, ev.Attention.Closed)
	}
	a.noteSessionEvent("work", SessionEvent{Type: eventCompletionSeen, Window: "w2", completionSeq: 3})
	if ev := lastEvent(*events); ev.Attention.Closed != AttentionClosedSeen {
		t.Errorf("a look published %s", ev.Attention.Closed)
	}
	a.noteSessionEvent("work", SessionEvent{Type: EventWindowClosed, Window: "w3"})
	if ev := lastEvent(*events); ev.Attention.Closed != AttentionClosedWindow {
		t.Errorf("the pane closing published %s", ev.Attention.Closed)
	}
	if a.snoozedCount() != 0 {
		t.Errorf("%d items are still asleep", a.snoozedCount())
	}
}

// TestOnlyOneWakeTimerRuns: the timer exists only while a timed snooze does,
// is set for the earliest, and wakes what is due.
func TestOnlyOneWakeTimerRuns(t *testing.T) {
	a, _, clock := lifecycleStore(t)
	timer := func() (*time.Timer, int64) {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.wakeTimer, a.wakeAt
	}
	if tm, _ := timer(); tm != nil {
		t.Fatal("a store with nothing snoozed has a timer")
	}
	a.noteSessionEvent("work", agentEvent("w1", "working", "errored", "", "a", 0, 0))
	a.noteSessionEvent("work", agentEvent("w2", "working", "errored", "", "b", 0, 0))
	a.noteSessionEvent("work", agentEvent("w3", "working", "errored", "", "c", 0, 0))
	items := openItems(t, a)
	now := clock.now()
	_ = snooze(t, a, items[0].ID, SnoozeUntilChange)
	if tm, _ := timer(); tm != nil {
		t.Fatal("a snooze until it changes set a timer")
	}
	late, early := now.Add(time.Hour).UnixNano(), now.Add(time.Minute).UnixNano()
	_ = snooze(t, a, items[1].ID, late)
	first, at := timer()
	if first == nil || at != late {
		t.Fatalf("the timer is set for %d, want %d", at, late)
	}
	_ = snooze(t, a, items[2].ID, early)
	if tm, at := timer(); tm == nil || at != early || tm == first {
		t.Fatalf("the timer is set for %d, want the earlier %d on a new timer", at, early)
	}

	// Time passes and the timer fires: the due item wakes and the timer
	// moves to the next one.
	clock.add(2 * time.Minute)
	a.wakeDue()
	if open := openItems(t, a); len(open) != 1 || open[0].ID != items[2].ID {
		t.Fatalf("after the first wake %+v is open", open)
	}
	if _, at := timer(); at != late {
		t.Errorf("the timer moved to %d, want %d", at, late)
	}
	a.mu.Lock()
	a.wakeLocked(items[1].ID)
	a.mu.Unlock()
	if tm, _ := timer(); tm != nil {
		t.Error("a timer outlived the last timed snooze")
	}
}

// TestSnoozeIsRefusedOnWhatWaitsForAnAnswer: a held approval, a plan, an ask
// and an outbox cannot be snoozed.
func TestSnoozeIsRefusedOnWhatWaitsForAnAnswer(t *testing.T) {
	for _, it := range []AttentionItem{
		{Kind: AttentionApproval, RequestID: "abc"},
		{Kind: AttentionPlan},
		{Kind: AttentionAsk, RequestID: "abc"},
		{Kind: AttentionOutbox},
	} {
		if ok, _ := attentionSnoozable(&it); ok {
			t.Errorf("%s (request %q) is snoozable", it.Kind, it.RequestID)
		}
	}
	for _, kind := range []string{AttentionFinished, AttentionErrored, AttentionMail, AttentionResume, AttentionApproval, AttentionQuestion} {
		if ok, why := attentionSnoozable(&AttentionItem{Kind: kind}); !ok {
			t.Errorf("%s is not snoozable: %s", kind, why)
		}
	}
}

// TestRestoreIsForTenSeconds: a dismiss or a snooze can be restored with the
// item's id and since for ten seconds, and not after.
func TestRestoreIsForTenSeconds(t *testing.T) {
	a, events, clock := lifecycleStore(t)
	a.noteSessionEvent("work", agentEvent("w1", "working", "done", "", "done", 0, 1))
	a.noteSessionEvent("work", agentEvent("w2", "working", "errored", "", "x", 0, 0))
	items := openItems(t, a)
	fin, errd := items[1], items[0]

	if _, ok := a.dismiss(fin.ID); !ok {
		t.Fatal("dismiss failed")
	}
	clock.add(9 * time.Second)
	got, verr := a.restore(fin.ID)
	if verr != nil || got.ID != fin.ID || got.Since != fin.Since {
		t.Fatalf("restore within 10s: %+v, %v", got, verr)
	}
	if ev := lastEvent(*events); ev.Action != AttentionOpened || ev.Attention.ID != fin.ID {
		t.Errorf("the restore published %s for %s", ev.Action, ev.Attention.ID)
	}
	if _, verr := a.restore(fin.ID); verr == nil {
		t.Error("a second restore of the same close went through")
	}

	_ = snooze(t, a, errd.ID, clock.now().Add(time.Hour).UnixNano())
	clock.add(10 * time.Second)
	if _, verr := a.restore(errd.ID); verr == nil || verr.Code != ErrVerbInvalidParams {
		t.Fatalf("a restore after 10s answered %v", verr)
	}
	// Wake still works after the undo window.
	a.mu.Lock()
	_, ok := a.wakeLocked(errd.ID)
	a.mu.Unlock()
	if !ok {
		t.Error("wake failed after the undo window")
	}

	// A newer item on the same key wins over a restore.
	a.dismiss(errd.ID)
	a.noteSessionEvent("work", agentEvent("w2", "errored", "errored", "", "again", 0, 0))
	if _, verr := a.restore(errd.ID); verr == nil {
		t.Error("a restore replaced the newer item about the same pane")
	}
}

// TestSnoozedItemsSurviveARestart: a snoozed finished item is saved with
// snoozed_until and comes back asleep; one whose time passed while the daemon
// was down wakes.
func TestSnoozedItemsSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attention", "items.json")
	a, _, clock := lifecycleStore(t)
	a.noteSessionEvent("work", agentEvent("w1", "working", "done", "", "one", 0, 1))
	a.noteSessionEvent("work", agentEvent("w2", "working", "done", "", "two", 0, 1))
	items := openItems(t, a)
	_ = snooze(t, a, items[0].ID, SnoozeUntilChange)
	_ = snooze(t, a, items[1].ID, clock.now().Add(time.Second).UnixNano())
	a.mu.Lock()
	data := a.encodeLocked()
	a.mu.Unlock()
	writeAttentionFile(path, data)

	clock.add(time.Minute)
	b, _, _ := lifecycleStore(t)
	b.now = clock.now
	b.load(path, func(string, string) bool { return true })
	if n := b.snoozedCount(); n != 2 {
		t.Fatalf("%d items came back asleep, want 2", n)
	}
	b.wakeDue()
	open := openItems(t, b)
	if len(open) != 1 || open[0].ID != items[1].ID {
		t.Fatalf("after the restart %+v is open, want %s", open, items[1].ID)
	}
	listed, _, _ := b.list(attentionQuery{snoozed: true})
	if len(listed) != 2 || listed[1].ID != items[0].ID || listed[1].SnoozedUntil != SnoozeUntilChange {
		t.Errorf("the listing after the restart is %+v", listed)
	}
}

// TestHostItemsAreSnoozedHereOnly: snoozing another machine's item hides it
// here until its time or until that machine changes it, and restore of a
// hidden host item works while the host has not changed it.
func TestHostItemsAreSnoozedHereOnly(t *testing.T) {
	a, _, clock := lifecycleStore(t)
	a.hostReplace("build", []AttentionItem{{ID: "7", Kind: AttentionFinished, Session: "ci", Window: "w", Seq: 4, Since: 1}})
	id := hostItemID("build", "7")
	if verr := snooze(t, a, id, clock.now().Add(time.Hour).UnixNano()); verr != nil {
		t.Fatalf("snooze: %v", verr)
	}
	// The host lists it again, unchanged: it stays asleep.
	a.hostReplace("build", []AttentionItem{{ID: "7", Kind: AttentionFinished, Session: "ci", Window: "w", Seq: 4, Since: 1}})
	if items := openItems(t, a); len(items) != 0 {
		t.Fatalf("an unchanged listing woke %+v", items)
	}
	// The host changes it: it wakes.
	a.hostApply("build", AttentionUpdated, AttentionItem{ID: "7", Kind: AttentionFinished, Session: "ci", Window: "w", Seq: 5, Since: 1, Count: 2})
	if items := openItems(t, a); len(items) != 1 || items[0].ID != id {
		t.Fatalf("a change on the host left %+v", items)
	}
	// Dismiss and restore.
	a.dismiss(id)
	if _, verr := a.restore(id); verr != nil {
		t.Fatalf("restore of a hidden host item: %v", verr)
	}
	// The host closing it while asleep drops it.
	_ = snooze(t, a, id, SnoozeUntilChange)
	a.hostApply("build", AttentionClosed, AttentionItem{ID: "7", Kind: AttentionFinished, Session: "ci", Seq: 6, Closed: AttentionClosedSeen})
	if a.snoozedCount() != 0 {
		t.Error("a host close left the item asleep here")
	}
}

// TestMarkAttentionOverTheWire drives the verb as the person: snooze, the
// listing, wake, unread and restore, and the refusals.
func TestMarkAttentionOverTheWire(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, a, b := twoWindowSession(t, d, "work")
	makeSessionWithWindow(t, d, "other")
	c := dialVerb(t, sp)
	tui := attachTUI(t, sp, "other")
	nonce := tui.HumanNonce()
	mark := func(params map[string]any) map[string]any {
		t.Helper()
		params["human_nonce"] = nonce
		return callP(c, t, "mark-attention", params)
	}

	setAgentState(t, c, "work", a, "working", "", "")
	setAgentState(t, c, "work", a, "done", "", "finished the refactor")
	items := waitAttention(t, c, "a finished turn", hasKind(AttentionFinished, a))
	id := items[0]["id"].(string)

	// Exactly one of the three lengths.
	mustRefuse(t, mark(map[string]any{"id": id, "action": "snooze"}), ErrVerbInvalidParams, "a snooze with no length")
	mustRefuse(t, mark(map[string]any{"id": id, "action": "snooze", "for_ms": 1000, "until_change": true}), ErrVerbInvalidParams, "a snooze with two lengths")
	mustRefuse(t, mark(map[string]any{"id": id, "action": "wake", "for_ms": 1000}), ErrVerbInvalidParams, "a length on wake")
	mustRefuse(t, mark(map[string]any{"id": id, "action": "snooze", "until": time.Now().Add(-time.Minute).UnixMilli()}), ErrVerbInvalidParams, "a snooze into the past")

	res := result(t, mark(map[string]any{"id": id, "action": "snooze", "for_ms": 3_600_000}))
	if res["type"] != "attention_marked" || res["id"] != id || res["snoozed_until"] == nil {
		t.Fatalf("snooze answered %v", res)
	}
	if items, _ := listAttention(t, c, ""); len(items) != 0 {
		t.Fatalf("the snoozed item is listed: %v", items)
	}
	if items, _ := listAttention(t, c, `{"include_snoozed":true}`); len(items) != 1 || items[0]["snoozed_until"] == nil {
		t.Fatalf("include_snoozed listed %v", items)
	}
	result(t, mark(map[string]any{"id": id, "action": "wake"}))
	if items, _ := listAttention(t, c, ""); len(items) != 1 || items[0]["id"] != id {
		t.Fatalf("after wake %v", items)
	}
	mustRefuse(t, mark(map[string]any{"id": id, "action": "wake"}), ErrVerbInvalidParams, "a wake of an item not asleep")

	// By pane and kind, rather than by id.
	result(t, mark(map[string]any{"session": "work", "window": a, "kind": AttentionFinished, "action": "snooze", "until_change": true}))
	result(t, mark(map[string]any{"id": id, "action": "restore"}))
	if items, _ := listAttention(t, c, ""); len(items) != 1 {
		t.Fatalf("restore of a snooze left %v", items)
	}

	// Dismiss marks the turn seen; unread brings it back, forgetting the
	// look, and the pane's next focus closes it again.
	result(t, c.call(t, fmt.Sprintf(`{"id":1,"verb":"dismiss-attention","params":{"id":%q,"human_nonce":%q}}`, id, nonce)))
	waitAttention(t, c, "the dismiss", isEmpty)
	res = result(t, mark(map[string]any{"session": "work", "window": a, "action": "unread"}))
	items = waitAttention(t, c, "the unread item", hasKind(AttentionFinished, a))
	if items[0]["marked_unread"] != true || res["id"] != items[0]["id"] {
		t.Fatalf("unread opened %v, answered %v", items, res)
	}
	for _, w := range sess.GetState().Windows {
		if w.ID == a && !sess.finishedUnread(&w) {
			t.Error("finished_unread is false after unread")
		}
	}
	st := sess.GetState()
	st.FocusedWindowID = a
	sess.UpdateState(st)
	waitAttention(t, c, "focusing the pane after unread", isEmpty)

	// Unread on a pane that never finished a turn says so.
	mustRefuse(t, mark(map[string]any{"session": "work", "window": b, "action": "unread"}), ErrVerbInvalidParams, "unread on a pane with no turn")

	// Restore of a dismiss brings the approval back only while the pane is
	// still blocked.
	setAgentState(t, c, "work", b, "needs_input", "approval", "ok?")
	items = waitAttention(t, c, "an approval", hasKind(AttentionApproval, b))
	bid := items[0]["id"].(string)
	result(t, c.call(t, fmt.Sprintf(`{"id":1,"verb":"dismiss-attention","params":{"id":%q,"human_nonce":%q}}`, bid, nonce)))
	result(t, mark(map[string]any{"id": bid, "action": "restore"}))
	waitAttention(t, c, "the restored approval", hasKind(AttentionApproval, b))
	result(t, c.call(t, fmt.Sprintf(`{"id":1,"verb":"dismiss-attention","params":{"id":%q,"human_nonce":%q}}`, bid, nonce)))
	setAgentState(t, c, "work", b, "working", "", "")
	mustRefuse(t, mark(map[string]any{"id": bid, "action": "restore"}), ErrVerbInvalidParams, "restore after the pane moved on")
}

// TestMarkAttentionRefusesAPaneWithACopiedNonce: a live nonce is the
// person's only from outside every pane. A call a pane made, holding the
// nonce of the client attached right now, is refused as not the person.
func TestMarkAttentionRefusesAPaneWithACopiedNonce(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	tui := attachTUI(t, sp, "work")
	raw := []byte(fmt.Sprintf(`{"id":"1","action":"wake","human_nonce":%q}`, tui.HumanNonce()))
	_, verr := d.verbMarkAttention(&connState{paneOnly: true}, raw)
	if verr == nil || verr.Code != ErrVerbNotHuman {
		t.Fatalf("a pane with the person's nonce answered %v, want %s", verr, ErrVerbNotHuman)
	}
	// The same nonce from outside every pane passes the proof and reaches
	// the item, which is not there.
	_, verr = d.verbMarkAttention(&connState{}, raw)
	if verr == nil || verr.Code != ErrVerbInvalidParams {
		t.Fatalf("the person answered %v, want %s", verr, ErrVerbInvalidParams)
	}
}
