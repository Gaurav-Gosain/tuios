//go:build !slim

package session

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// TestLifecycleEventsMatchHeadlessAndAttached is the core guarantee: a
// subscriber sees the same event stream for the same operation whether the
// daemon performed it headlessly or an attached TUI did. Previously the attached
// case produced no window lifecycle events at all.
func TestLifecycleEventsMatchHeadlessAndAttached(t *testing.T) {
	d, sp := startTestDaemon(t)

	// Headless: no client attached, the daemon mutates its own state.
	makeSessionWithWindow(t, d, "headless")
	hsub := subscribeTo(t, sp, "headless", lifecycleTypes...)

	out, verr := d.verbNewWindow(nil, json.RawMessage(`{"session":"headless","name":"build"}`))
	if verr != nil {
		t.Fatalf("verbNewWindow: %v", verr)
	}
	createdID := out.(map[string]any)["window_id"].(string)
	if _, verr := d.verbCloseWindow(nil, json.RawMessage(`{"session":"headless","window":"`+createdID+`"}`)); verr != nil {
		t.Fatalf("verbCloseWindow: %v", verr)
	}
	// Creating focuses the new window, and closing it hands focus back, so each
	// mutation raises its lifecycle event plus the changes it caused. Naming is
	// not a mutation of its own: a window is created with the name it was asked
	// for, so window-created carries it and no retitle follows.
	headlessEvents := collectEvents(t, hsub, 4, 3*time.Second)

	// Attached: a client is attached, and the same two verbs run. There is no
	// second implementation for them to reach any more, so what this asserts is
	// that attaching a client does not change the event stream, including that
	// the client echoing the state back raises nothing extra.
	attached := makeSessionWithWindow(t, d, "attached")
	tui := attachSyncingTUI(t, d, attached)

	asub := subscribeTo(t, sp, "attached", lifecycleTypes...)

	out, verr = d.verbNewWindow(nil, json.RawMessage(`{"session":"attached","name":"build"}`))
	if verr != nil {
		t.Fatalf("verbNewWindow: %v", verr)
	}
	newID := out.(map[string]any)["window_id"].(string)
	tui.sync(attached.GetState())

	if _, verr := d.verbCloseWindow(nil, json.RawMessage(`{"session":"attached","window":"`+newID+`"}`)); verr != nil {
		t.Fatalf("verbCloseWindow: %v", verr)
	}
	tui.sync(attached.GetState())

	attachedEvents := collectEvents(t, asub, 4, 3*time.Second)

	if got, want := eventTypes(headlessEvents), eventTypes(attachedEvents); !reflect.DeepEqual(got, want) {
		t.Fatalf("event streams differ:\n headless = %v\n attached = %v", got, want)
	}
	if got := eventTypes(headlessEvents); !reflect.DeepEqual(got, []string{
		EventWindowCreated, EventWindowFocused, EventWindowClosed, EventWindowFocused,
	}) {
		t.Fatalf("unexpected event sequence: %v", got)
	}

	// Payload shape must match too, not just the type sequence.
	for i := range headlessEvents {
		h, a := headlessEvents[i], attachedEvents[i]
		for _, field := range []string{"session", "window", "pty_id", "seq", "time"} {
			if _, ok := h[field]; ok != hasKey(a, field) {
				t.Errorf("event %d (%v): field %q present=%v headless, %v attached",
					i, h["type"], field, ok, hasKey(a, field))
			}
		}
		// The default title carries the window's own UUID, so two windows never
		// share one. What must match is that both paths report a title at all.
		if h["type"] == EventWindowCreated {
			if h["title"] == "" || a["title"] == "" {
				t.Errorf("window-created title = %q headless, %q attached, want both set", h["title"], a["title"])
			}
			// The name asked for arrives with the creation rather than in a
			// retitle behind it. A subscriber that has to wait for a second
			// event to learn what a window is called sees it under its
			// generated title first, and anything matching on the name misses
			// the window entirely if it only ever looks at creations.
			if h["title"] != "build" || a["title"] != "build" {
				t.Errorf("window-created title = %q headless, %q attached, want the name it was created with",
					h["title"], a["title"])
			}
		}
	}
}

// TestTUIDrivenWindowLifecycleIsObserved is the regression test for the reported
// bug: a window created and then closed by an attached TUI, with no control-plane
// verb involved at all, must still reach a subscriber.
func TestTUIDrivenWindowLifecycleIsObserved(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	tui := &syncingTUI{d: d, sess: sess}
	tui.conn, _ = newFakeTUI(t, d, sess.ID)

	sub := subscribeTo(t, sp, "work", EventWindowCreated, EventWindowClosed)

	// The TUI creates a window on its own (a human pressing the new-window key).
	state := sess.GetState()
	newTUIWindow(t, sess, state, "human-window", "shell")
	tui.sync(state)

	// ...and then closes it.
	state = sess.GetState()
	state.Windows = state.Windows[:len(state.Windows)-1]
	tui.sync(state)

	events := collectEvents(t, sub, 2, 3*time.Second)
	if got := eventTypes(events); !reflect.DeepEqual(got, []string{EventWindowCreated, EventWindowClosed}) {
		t.Fatalf("event types = %v, want created then closed", got)
	}
	for _, ev := range events {
		if ev["window"] != "human-window" {
			t.Errorf("event %v carried window %v, want human-window", ev["type"], ev["window"])
		}
		if ev["pty_id"] == nil || ev["pty_id"] == "" {
			t.Errorf("event %v missing pty_id", ev["type"])
		}
	}
	expectNoMoreEvents(t, sub, 300*time.Millisecond)
}

// TestLifecycleEventsFireExactlyOnce verifies the headless path does not
// double-emit now that it converges through the same diff as the TUI path, and
// that a redundant state sync (identical state pushed again) emits nothing.
func TestLifecycleEventsFireExactlyOnce(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	sub := subscribeTo(t, sp, "work", lifecycleTypes...)

	if _, err := sess.AddDaemonWindow("only", nil); err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}

	// One create raises exactly one window-created (plus the focus change it
	// causes), never two of either.
	events := collectEvents(t, sub, 2, 3*time.Second)
	if got := eventTypes(events); !reflect.DeepEqual(got, []string{EventWindowCreated, EventWindowFocused}) {
		t.Fatalf("event types = %v, want one window-created then one window-focused", got)
	}
	expectNoMoreEvents(t, sub, 300*time.Millisecond)

	// Re-syncing the very same state is a no-op for the event stream.
	tui, _ := newFakeTUI(t, d, sess.ID)
	(&syncingTUI{d: d, sess: sess, conn: tui}).sync(sess.GetState())
	expectNoMoreEvents(t, sub, 300*time.Millisecond)
}

// TestLifecycleEventSequenceIsMonotonic verifies events keep strictly increasing
// sequence numbers across a mix of headless operations.
func TestLifecycleEventSequenceIsMonotonic(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	sub := subscribeTo(t, sp, "work", lifecycleTypes...)

	a, err := sess.AddDaemonWindow("a", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	if err := sess.RenameDaemonWindow(a.ID, "renamed"); err != nil {
		t.Fatalf("RenameDaemonWindow: %v", err)
	}
	if err := sess.MoveDaemonWindowToWorkspace(a.ID, 3); err != nil {
		t.Fatalf("MoveDaemonWindowToWorkspace: %v", err)
	}
	if err := sess.SwitchDaemonWorkspace(3); err != nil {
		t.Fatalf("SwitchDaemonWorkspace: %v", err)
	}
	if err := sess.SetDaemonWindowMinimized(a.ID, true); err != nil {
		t.Fatalf("SetDaemonWindowMinimized: %v", err)
	}

	events := collectEvents(t, sub, 6, 3*time.Second)
	want := []string{
		EventWindowCreated,
		EventWindowFocused,
		EventWindowRetitled,
		EventWindowMoved,
		EventWorkspaceSwitched,
		EventWindowMinimized,
	}
	if got := eventTypes(events); !reflect.DeepEqual(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}

	var prev float64
	for i, ev := range events {
		seq, ok := ev["seq"].(float64)
		if !ok {
			t.Fatalf("event %d has no numeric seq: %v", i, ev["seq"])
		}
		if seq <= prev {
			t.Fatalf("seq not monotonic at event %d: %v after %v", i, seq, prev)
		}
		prev = seq
	}

	if events[3]["workspace"] != float64(3) {
		t.Errorf("window-moved workspace = %v, want 3", events[3]["workspace"])
	}
	if events[4]["workspace"] != float64(3) {
		t.Errorf("workspace-switched workspace = %v, want 3", events[4]["workspace"])
	}
	if events[2]["title"] != "renamed" {
		t.Errorf("window-retitled title = %v, want renamed", events[2]["title"])
	}
}

// TestStaleClientSyncRaisesNoPhantomEvents ties state versioning back to the
// event stream. A daemon-side creation emits window-created once. A stale client
// sync that predates it used to replace the whole state, which the diff read as
// the window being closed and, on the client's next sync, created again. Now the
// creation is preserved through reconciliation, so the sync is a no-op and the
// stream stays silent.
func TestStaleClientSyncRaisesNoPhantomEvents(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, err := d.manager.CreateSession("phantom", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sub := subscribeTo(t, sp, "phantom", lifecycleTypes...)

	stale := sess.GetState()
	stale.BaseVersion = stale.Version
	stale.Version = 0

	win, err := sess.AddDaemonWindow("headless", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}

	events := collectEvents(t, sub, 2, 3*time.Second)
	if types := eventTypes(events); len(types) != 2 ||
		types[0] != EventWindowCreated || types[1] != EventWindowFocused {
		t.Fatalf("events = %v, want window-created then window-focused", types)
	}
	if events[0]["window"] != win.ID {
		t.Fatalf("created window = %v, want %v", events[0]["window"], win.ID)
	}

	sess.UpdateState(stale)
	expectNoMoreEvents(t, sub, 300*time.Millisecond)
}

// TestRestoredSessionRaisesWindowCreated pins the documented resurrection
// behavior: restoring a session raises session-created and then a window-created
// per restored window, because from a subscriber's point of view those windows
// come into existence at that moment.
func TestRestoredSessionRaisesWindowCreated(t *testing.T) {
	d, sp := startTestDaemon(t)

	sub := dialVerb(t, sp)
	result(t, sub.call(t, `{"id":1,"verb":"subscribe","params":{"session":"revived","types":["session-created","window-created"]}}`))

	if _, err := d.restoreSession(&SessionState{
		Name:             "revived",
		Windows:          []WindowState{{ID: "w1", Title: "one", Width: 80, Height: 24, Workspace: 1}},
		CurrentWorkspace: 1,
		Width:            80,
		Height:           24,
	}); err != nil {
		t.Fatalf("restoreSession: %v", err)
	}

	events := collectEvents(t, sub, 2, 3*time.Second)
	if got := eventTypes(events); !reflect.DeepEqual(got, []string{EventSessionCreated, EventWindowCreated}) {
		t.Fatalf("event types = %v, want session-created then window-created", got)
	}
	if events[1]["window"] != "w1" {
		t.Errorf("window-created window = %v, want w1", events[1]["window"])
	}
}
