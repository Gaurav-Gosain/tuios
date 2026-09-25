package session

import (
	"reflect"
	"testing"
)

// bareSessionWithWindow builds a session with one window but no daemon socket, so
// a session can be exercised directly without any network.
func bareSessionWithWindow(t *testing.T) (*Session, string) {
	t.Helper()
	t.Cleanup(useResurrectionDir(t.TempDir()))
	sess, err := NewSession("stall", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	win, err := sess.AddDaemonWindow("Window", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	t.Cleanup(sess.Stop)
	return sess, win.ID
}

func agentStateOf(t *testing.T, sess *Session, windowID string) AgentState {
	t.Helper()
	for _, w := range sess.GetState().Windows {
		if w.ID == windowID {
			return w.AgentState
		}
	}
	t.Fatalf("window %s not found", windowID)
	return AgentStateNone
}

// TestAClientSyncKeepsTheDaemonsWindowFields: no client sets these fields, so
// a client push that omits them must not wipe them. The daemon carries each
// over from its own state by window id.
func TestAClientSyncKeepsTheDaemonsWindowFields(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		canonical, incoming, want WindowState
	}{
		{
			name:      "agent state and message",
			canonical: WindowState{ID: "w1", AgentState: AgentStateWorking, AgentMessage: "building", AgentStateAt: 5},
			incoming:  WindowState{ID: "w1"},
			want:      WindowState{ID: "w1", AgentState: AgentStateWorking, AgentMessage: "building", AgentStateAt: 5},
		},
		{
			name:      "agent kind and session id",
			canonical: WindowState{ID: "w1", AgentState: AgentStateNeedsInput, AgentKind: "approval", AgentSessionID: "s1"},
			incoming:  WindowState{ID: "w1", AgentState: AgentStateNeedsInput, AgentMessage: "x"},
			want:      WindowState{ID: "w1", AgentState: AgentStateNeedsInput, AgentMessage: "x", AgentKind: "approval", AgentSessionID: "s1"},
		},
		{
			name:      "finished turn count",
			canonical: WindowState{ID: "w1", CompletionSeq: 1},
			incoming:  WindowState{ID: "w1"},
			want:      WindowState{ID: "w1", CompletionSeq: 1},
		},
		{
			name:      "foreground command",
			canonical: WindowState{ID: "w1", ForegroundCmd: "nvim"},
			incoming:  WindowState{ID: "w1"},
			want:      WindowState{ID: "w1", ForegroundCmd: "nvim"},
		},
		{
			name:      "exited foreground command",
			canonical: WindowState{ID: "w1"},
			incoming:  WindowState{ID: "w1"},
			want:      WindowState{ID: "w1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			incoming := &SessionState{Windows: []WindowState{tc.incoming}}
			retainDaemonExclusive(incoming, &SessionState{Windows: []WindowState{tc.canonical}})
			if got := incoming.Windows[0]; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("after a client sync:\n got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}
