package session

import (
	"encoding/json"

	"reflect"
	"testing"
	"time"
)

// collectEvents reads event lines off a subscribed connection until it has want
// of them or the read deadline expires. It returns what it managed to read, so a
// caller can assert on "exactly N and no more" as well as on the contents.
func collectEvents(t *testing.T, c *verbConn, want int, wait time.Duration) []map[string]any {
	t.Helper()
	var got []map[string]any
	deadline := time.Now().Add(wait)
	for len(got) < want {
		_ = c.conn.SetReadDeadline(deadline)
		line, err := c.r.ReadBytes('\n')
		if err != nil {
			break
		}
		var ev map[string]any
		if err := json.Unmarshal(line, &ev); err != nil {
			t.Fatalf("decode event %q: %v", string(line), err)
		}
		got = append(got, ev)
	}
	return got
}

// expectNoMoreEvents fails if any further event line arrives within grace. This
// is how the exactly-once assertions are made: the expected events are drained
// first, then the stream must be silent.
func expectNoMoreEvents(t *testing.T, c *verbConn, grace time.Duration) {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(grace))
	line, err := c.r.ReadBytes('\n')
	if err == nil {
		t.Fatalf("unexpected extra event: %s", string(line))
	}
}

func eventTypes(events []map[string]any) []string {
	types := make([]string, 0, len(events))
	for _, ev := range events {
		s, _ := ev["type"].(string)
		types = append(types, s)
	}
	return types
}

// subscribeTo opens an event stream filtered to one session and the given types.
func subscribeTo(t *testing.T, sp, session string, types ...string) *verbConn {
	t.Helper()
	c := dialVerb(t, sp)
	params := map[string]any{"session": session}
	if len(types) > 0 {
		params["types"] = types
	}
	req, err := json.Marshal(map[string]any{"id": 1, "verb": "subscribe", "params": params})
	if err != nil {
		t.Fatalf("marshal subscribe: %v", err)
	}
	ack := result(t, c.call(t, string(req)))
	if ack["type"] != EventSubscribed {
		t.Fatalf("subscribe ack type = %v, want subscribed", ack["type"])
	}
	return c
}

// lifecycleTypes are the window lifecycle event types under test. PTY-driven
// types (output, bell, window-exit, mode-changed) are excluded so a live shell's
// startup chatter cannot make these tests flaky.
var lifecycleTypes = []string{
	EventWindowCreated,
	EventWindowClosed,
	EventWindowRetitled,
	EventWindowFocused,
	EventWindowMoved,
	EventWindowMinimized,
	EventWindowRestored,
	EventWorkspaceSwitched,
}

// syncingTUI is a fake attached TUI that behaves like the real one: it applies a
// routed remote command to the session itself and pushes the resulting state
// back to the daemon with UpdateState, which is the convergence point the
// lifecycle events are derived from. It never emits events itself.
type syncingTUI struct {
	d    *Daemon
	sess *Session
	conn *connState
}

// attachSyncingTUI registers the fake TUI and serves routed remote commands with
// apply until the test ends.
func attachSyncingTUI(t *testing.T, d *Daemon, sess *Session, apply func(state *SessionState) *CommandResultPayload) *syncingTUI {
	t.Helper()
	tui, clientSide := newFakeTUI(t, d, sess.ID)
	s := &syncingTUI{d: d, sess: sess, conn: tui}

	done := make(chan struct{})
	t.Cleanup(func() { close(done) })

	go func() {
		for {
			msg, _, err := ReadMessageWithCodec(clientSide)
			if err != nil {
				return
			}
			select {
			case <-done:
				return
			default:
			}
			var rc RemoteCommandPayload
			if err := msg.ParsePayloadWithCodec(&rc, DefaultCodec()); err != nil {
				return
			}
			state := sess.GetState()
			res := apply(state)
			// The real TUI owns the state and syncs it back; do the same, so the
			// daemon converges through the one path that raises the events.
			s.sync(state)
			res.RequestID = rc.RequestID
			resMsg, err := NewMessage(MsgCommandResult, res)
			if err != nil {
				return
			}
			_ = d.handleCommandResult(tui, resMsg)
		}
	}()
	return s
}

// sync pushes a state snapshot to the daemon exactly as a TUI client does, via
// the daemon's update-state handler.
func (s *syncingTUI) sync(state *SessionState) {
	msg, err := NewMessageWithCodec(MsgUpdateState, state, DefaultCodec())
	if err != nil {
		return
	}
	_ = s.d.handleUpdateState(s.conn, msg)
}

// newTUIWindow builds the window a TUI would add for a new-window command,
// including a live PTY, and appends it to state with focus, mirroring
// AddDaemonWindow.
func newTUIWindow(t *testing.T, sess *Session, state *SessionState, id, title string) {
	t.Helper()
	pty, err := sess.CreatePTY(id, 78, 22, nil)
	if err != nil {
		t.Fatalf("CreatePTY: %v", err)
	}
	ws := state.CurrentWorkspace
	if ws < 1 {
		ws = 1
		state.CurrentWorkspace = 1
	}
	state.Windows = append(state.Windows, WindowState{
		ID: id, Title: title, Width: 80, Height: 24, Workspace: ws, PTYID: pty.ID,
	})
	state.FocusedWindowID = id
	if state.WorkspaceFocus == nil {
		state.WorkspaceFocus = make(map[int]string)
	}
	state.WorkspaceFocus[ws] = id
}

// TestLifecycleEventsMatchHeadlessAndAttached is the core guarantee: a
// subscriber sees the same event stream for the same operation whether the
// daemon performed it headlessly or an attached TUI did. Previously the attached
// case produced no window lifecycle events at all.
func TestLifecycleEventsMatchHeadlessAndAttached(t *testing.T) {
	d, sp := startTestDaemon(t)

	// Headless: no client attached, the daemon mutates its own state.
	headless := makeSessionWithWindow(t, d, "headless")
	hsub := subscribeTo(t, sp, "headless", lifecycleTypes...)

	created, err := headless.AddDaemonWindow("build", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	if _, err := headless.CloseDaemonWindow(created.ID); err != nil {
		t.Fatalf("CloseDaemonWindow: %v", err)
	}
	// Creating focuses the new window and closing it hands focus back, so each
	// mutation raises its lifecycle event plus the focus change it caused.
	headlessEvents := collectEvents(t, hsub, 4, 3*time.Second)

	// Attached: an attached TUI performs the same two mutations and syncs.
	attached := makeSessionWithWindow(t, d, "attached")
	firstWindow := attached.GetState().Windows[0].ID

	var closeTarget string
	tui := attachSyncingTUI(t, d, attached, func(state *SessionState) *CommandResultPayload {
		if closeTarget == "" {
			newTUIWindow(t, attached, state, "tui-window-id", "build")
			return &CommandResultPayload{Success: true, Data: map[string]any{"window_id": "tui-window-id"}}
		}
		for i := range state.Windows {
			if state.Windows[i].ID != closeTarget {
				continue
			}
			state.Windows = append(state.Windows[:i], state.Windows[i+1:]...)
			state.FocusedWindowID = firstWindow
			break
		}
		return &CommandResultPayload{Success: true}
	})
	_ = tui

	asub := subscribeTo(t, sp, "attached", lifecycleTypes...)

	if _, verr := d.verbNewWindow(nil, json.RawMessage(`{"session":"attached","name":"build"}`)); verr != nil {
		t.Fatalf("verbNewWindow: %v", verr)
	}
	closeTarget = "tui-window-id"
	if _, verr := d.verbCloseWindow(nil, json.RawMessage(`{"session":"attached","window":"tui-window-id"}`)); verr != nil {
		t.Fatalf("verbCloseWindow: %v", verr)
	}
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
		if h["type"] == EventWindowCreated && h["title"] != a["title"] {
			t.Errorf("window-created title = %v headless, %v attached", h["title"], a["title"])
		}
	}
}

func hasKey(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
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

// TestDiffLifecycleOrdering pins the ordering the diff produces for a mutation
// that changes several things at once: closes before creates, and the focus
// change last so a consumer building a model from the stream already knows about
// the window being focused.
func TestDiffLifecycleOrdering(t *testing.T) {
	before := snapshotLifecycle(&SessionState{
		Windows: []WindowState{
			{ID: "gone", PTYID: "pty-gone", Workspace: 1},
			{ID: "stays", PTYID: "pty-stays", Workspace: 1},
		},
		FocusedWindowID:  "gone",
		CurrentWorkspace: 1,
	})
	after := snapshotLifecycle(&SessionState{
		Windows: []WindowState{
			{ID: "stays", PTYID: "pty-stays", Workspace: 2, CustomName: "named", Minimized: true},
			{ID: "fresh", PTYID: "pty-fresh", Workspace: 2, Title: "sh"},
		},
		FocusedWindowID:  "fresh",
		CurrentWorkspace: 2,
	})

	var got []string
	for _, ev := range diffLifecycle(before, after) {
		got = append(got, ev.Type)
	}
	want := []string{
		EventWindowClosed,
		EventWindowCreated,
		EventWindowRetitled,
		EventWindowMoved,
		EventWindowMinimized,
		EventWorkspaceSwitched,
		EventWindowFocused,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diff order = %v, want %v", got, want)
	}
}

// TestDiffLifecycleIgnoresNoise verifies changes that are not window lifecycle
// changes raise nothing, so a TUI syncing on every render does not flood the
// event stream.
func TestDiffLifecycleIgnoresNoise(t *testing.T) {
	base := []WindowState{{ID: "w", PTYID: "p", Workspace: 1, Title: "sh", X: 0, Y: 0, Width: 80, Height: 24}}
	before := snapshotLifecycle(&SessionState{Windows: base, FocusedWindowID: "w", CurrentWorkspace: 1})

	moved := []WindowState{{ID: "w", PTYID: "p", Workspace: 1, Title: "sh", X: 10, Y: 5, Width: 40, Height: 12, Z: 3, IsAltScreen: true}}
	after := snapshotLifecycle(&SessionState{Windows: moved, FocusedWindowID: "w", CurrentWorkspace: 1})

	if events := diffLifecycle(before, after); len(events) != 0 {
		t.Fatalf("geometry/z/alt-screen changes raised events: %+v", events)
	}
}

// TestDiffLifecycleIgnoresShellTitle verifies a shell-driven Title change raises
// nothing from the diff. The per-PTY emitter already reports OSC title changes,
// so deriving them here as well would report the same change twice.
func TestDiffLifecycleIgnoresShellTitle(t *testing.T) {
	before := snapshotLifecycle(&SessionState{
		Windows:          []WindowState{{ID: "w", PTYID: "p", Workspace: 1, Title: "bash"}},
		FocusedWindowID:  "w",
		CurrentWorkspace: 1,
	})
	after := snapshotLifecycle(&SessionState{
		Windows:          []WindowState{{ID: "w", PTYID: "p", Workspace: 1, Title: "vim"}},
		FocusedWindowID:  "w",
		CurrentWorkspace: 1,
	})

	if events := diffLifecycle(before, after); len(events) != 0 {
		t.Fatalf("shell title change raised events: %+v", events)
	}
}

// TestUpdateStateFromDisconnectedClientStillEmits guards the wiring: events must
// come from the daemon's update-state handler, not from anything TUI-side.
func TestUpdateStateFromDisconnectedClientStillEmits(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	sub := subscribeTo(t, sp, "work", EventWindowCreated)

	state := sess.GetState()
	state.Windows = append(state.Windows, WindowState{ID: "added", PTYID: "pty-added", Workspace: 1, Title: "x"})
	sess.UpdateState(state)

	events := collectEvents(t, sub, 1, 3*time.Second)
	if len(events) != 1 || events[0]["window"] != "added" {
		t.Fatalf("events = %v, want one window-created for 'added'", events)
	}
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
