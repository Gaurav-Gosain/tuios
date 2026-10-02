package session

import (
	"net"

	"reflect"
	"testing"
)

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
