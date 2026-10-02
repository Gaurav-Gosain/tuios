package session

import (
	"encoding/json"
	"net"

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

// syncingTUI is a fake attached TUI that behaves like the real one: it receives
// the daemon's state pushes and echoes the state back with UpdateState, which is
// what a real client does after absorbing a daemon-side change into its layout.
// It never emits events itself.
type syncingTUI struct {
	d    *Daemon
	sess *Session
	conn *connState
}

// attachSyncingTUI registers the fake TUI and drains whatever the daemon pushes
// at it, so a push can never block a mutation for the rest of the test.
func attachSyncingTUI(t *testing.T, d *Daemon, sess *Session) *syncingTUI {
	t.Helper()
	tui, clientSide := newFakeTUI(t, d, sess.ID)
	s := &syncingTUI{d: d, sess: sess, conn: tui}

	go func() {
		for {
			if _, err := ReadMessage(clientSide); err != nil {
				return
			}
		}
	}()
	return s
}

// sync pushes a state snapshot to the daemon exactly as a TUI client does, via
// the daemon's update-state handler.
func (s *syncingTUI) sync(state *SessionState) {
	msg, err := NewMessage(MsgUpdateState, state)
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

func hasKey(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
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

// newFakeTUI registers a connState that looks like an attached TUI client and
// returns it along with the client-side pipe end the test drives.
func newFakeTUI(t *testing.T, d *Daemon, sessionID string) (*connState, net.Conn) {
	t.Helper()
	serverSide, clientSide := net.Pipe()
	tui := &connState{
		conn:             serverSide,
		clientID:         "fake-tui",
		done:             make(chan struct{}),
		ptySubscriptions: make(map[string]struct{}),
		sessionID:        sessionID,
		isTUIClient:      true,
		// A real client is marked attached once its attach reply is written,
		// and nothing is sent to it before that. A hand-built one has to say so
		// too, or the daemon correctly treats it as still attaching and never
		// speaks to it. See connState.attached.
		attached: true,
	}
	d.clientsMu.Lock()
	d.clients[tui.clientID] = tui
	d.clientsMu.Unlock()
	t.Cleanup(func() { _ = clientSide.Close(); _ = serverSide.Close() })
	return tui, clientSide
}

// TestDiffLifecycleAttentionDetail verifies a needs_input window that keeps its
// state but changes kind or message raises the internal attention-detail event,
// never an agent-state event, and that a window in another state raises
// nothing for the same change.
func TestDiffLifecycleAttentionDetail(t *testing.T) {
	win := func(state AgentState, kind, msg string) lifecycleSnapshot {
		return snapshotLifecycle(&SessionState{Windows: []WindowState{{
			ID: "w", PTYID: "p", Workspace: 1, AgentState: state, AgentKind: kind, AgentMessage: msg, CompletionSeq: 2,
		}}})
	}
	events := diffLifecycle(win(AgentStateNeedsInput, "question", "which branch?"), win(AgentStateNeedsInput, "approval", "which branch?"))
	if len(events) != 1 || events[0].Type != eventAttentionDetail || events[0].hookKind != "approval" {
		t.Fatalf("a kind change raised %+v", events)
	}
	if events[0].prevCompletionSeq != events[0].completionSeq {
		t.Errorf("the detail event could read as a finished turn: %+v", events[0])
	}
	events = diffLifecycle(win(AgentStateNeedsInput, "approval", "a"), win(AgentStateNeedsInput, "approval", "b"))
	if len(events) != 1 || events[0].Type != eventAttentionDetail || events[0].hookMessage != "b" {
		t.Fatalf("a message change raised %+v", events)
	}
	if events := diffLifecycle(win(AgentStateWorking, "", "a"), win(AgentStateWorking, "", "b")); len(events) != 0 {
		t.Fatalf("a message change while working raised %+v", events)
	}
	if events := diffLifecycle(win(AgentStateNeedsInput, "approval", "a"), win(AgentStateNeedsInput, "approval", "a")); len(events) != 0 {
		t.Fatalf("an unchanged report raised %+v", events)
	}
}
