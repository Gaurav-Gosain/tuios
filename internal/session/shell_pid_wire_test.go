package session

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// WindowState.ShellPID is the client's only second source for the directory a
// pane reports over OSC 7, so what matters is not that the field exists but
// that it arrives. These drive a real daemon over a real unix socket, because
// the transport is gob and gob is where a field quietly does not travel.

// syncWatcher collects the session states a client is pushed.
type syncWatcher struct {
	mu     sync.Mutex
	states []*SessionState
}

func (w *syncWatcher) add(s *SessionState, _, _ string) {
	w.mu.Lock()
	w.states = append(w.states, s)
	w.mu.Unlock()
}

// await waits for a state the predicate accepts and returns it.
func (w *syncWatcher) await(t *testing.T, what string, ok func(*SessionState) bool) *SessionState {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		w.mu.Lock()
		for _, s := range w.states {
			if ok(s) {
				w.mu.Unlock()
				return s
			}
		}
		w.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// stateWindow finds a window in a state, or fails.
func stateWindow(t *testing.T, s *SessionState, id string) WindowState {
	t.Helper()
	for _, w := range s.Windows {
		if w.ID == id {
			return w
		}
	}
	t.Fatalf("no window %s in the state; it holds %d", id, len(s.Windows))
	return WindowState{}
}

// TestAZeroShellPidSurvivesTheSocket is the gob trap, asked directly. Zero is
// the answer that leaves a pane's file actions alone, so a zero that arrives as
// anything else is a pane called a liar for nothing, and a zero the daemon
// replaces with a stale pid is a pane checked against the wrong process.
//
// It is asked over the socket rather than in a unit test because gob is where
// this goes wrong: it omits zero values, and what a decoder does with a field
// the encoder omitted is the question.
func TestAZeroShellPidSurvivesTheSocket(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "zeroes")

	client := attachTestClient(t, "zeroes")
	var got syncWatcher
	client.OnStateSync(got.add)

	// A window the daemon has no process for. Clients do not create windows, so
	// this is put in the daemon's own state, which is where a pane with a dead
	// PTY would leave one.
	const ghost = "ghostwindow01"
	if err := sess.mutateState(func(st *SessionState) error {
		st.Windows = append(st.Windows, WindowState{ID: ghost, Width: 40, Height: 20, Workspace: 1})
		return nil
	}); err != nil {
		t.Fatalf("mutateState: %v", err)
	}

	s := got.await(t, "the state holding the window with no process", func(s *SessionState) bool {
		for _, w := range s.Windows {
			if w.ID == ghost {
				return true
			}
		}
		return false
	})
	if pid := stateWindow(t, s, ghost).ShellPID; pid != 0 {
		t.Fatalf("a window with no process arrived carrying shell pid %d, want 0", pid)
	}
}

// TestAClientPushDoesNotStripTheShellPid is the merge, over the wire. No client
// ever sets the pid, so every push a client makes reports zero for every pane.
// If the daemon took that at face value the corroboration would go out on the
// first keystroke and come back two seconds later, forever.
func TestAClientPushDoesNotStripTheShellPid(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "pushes")

	state := sess.GetState()
	winID := state.Windows[0].ID
	want := sess.GetPTY(state.Windows[0].PTYID).ShellPID()
	if want <= 0 {
		t.Fatal("the daemon's own PTY reports no shell pid")
	}

	client := attachTestClient(t, "pushes")

	// What a client really pushes: its own view, with no pid on any pane, and
	// something changed so the push is not a no-op.
	push := sess.GetState()
	for i := range push.Windows {
		push.Windows[i].ShellPID = 0
	}
	push.Windows[0].CustomName = "renamed by the client"
	if err := client.UpdateState(push); err != nil {
		t.Fatalf("UpdateState: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		after := sess.GetState()
		w := stateWindow(t, after, winID)
		if w.CustomName == "renamed by the client" {
			if w.ShellPID != want {
				t.Fatalf("a client push left shell pid %d, want %d", w.ShellPID, want)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the daemon never applied the client's push")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestTheShellPidIsNotWrittenToResurrectionState is why the field is json:"-".
// Resurrection state is JSON on disk and outlives the processes it describes.
// A pid saved there names whatever process later took the number, and a client
// restoring the session would corroborate its pane against a stranger: a pane
// telling the truth would lose its file actions, and one lying could keep them.
func TestTheShellPidIsNotWrittenToResurrectionState(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(useResurrectionDir(dir))

	sess, err := NewSession("saved", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(sess.Stop)
	if _, err := sess.AddDaemonWindow("Window", nil); err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	if err := sess.mutateState(func(st *SessionState) error {
		st.Windows[0].ShellPID = 424242
		return nil
	}); err != nil {
		t.Fatalf("mutateState: %v", err)
	}

	if err := SaveSessionForResurrection(sess.ResurrectionState()); err != nil {
		t.Fatalf("SaveSessionForResurrection: %v", err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("nothing was saved: %v %v", files, err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "424242") {
			t.Fatalf("%s holds the shell pid; a pid on disk outlives its process", f)
		}
	}

	loaded, err := LoadResurrectionState("saved")
	if err != nil {
		t.Fatalf("LoadResurrectionState: %v", err)
	}
	if pid := loaded.Windows[0].ShellPID; pid != 0 {
		t.Fatalf("a restored window carries shell pid %d, want 0", pid)
	}
}

// oldWindowState is a WindowState as a peer that predates ShellPID encodes one.
// gob matches struct fields by name, so encoding this and decoding a
// WindowState is what an older daemon on the wire looks like.
type oldWindowState struct {
	ID            string
	Title         string
	PTYID         string
	Width         int
	Height        int
	Workspace     int
	ForegroundCmd string
}

// TestAPeerThatNeverHeardOfTheFieldDecodesAsUnknown is why the protocol version
// stays at 3. No message type moved, so the only question is what gob does with
// a field the sender's type does not have, and the answer has to be zero: zero
// is "nobody knows", which leaves a pane's file actions exactly as they were.
// Anything else would call a pane a liar on the strength of a missing field.
func TestAPeerThatNeverHeardOfTheFieldDecodesAsUnknown(t *testing.T) {
	data, err := encodePayload(&oldWindowState{
		ID: "w1", Title: "shell", PTYID: "pty-1", Width: 80, Height: 24,
		Workspace: 1, ForegroundCmd: "nvim",
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var got WindowState
	if err := decodePayload(data, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != "w1" || got.ForegroundCmd != "nvim" {
		t.Fatalf("the rest of the window did not survive: %+v", got)
	}
	if got.ShellPID != 0 {
		t.Fatalf("a peer that sent no pid decoded as %d, want 0", got.ShellPID)
	}
}

// TestThePollRestoresAShellPidThatWentMissing is the detector's half of the
// contract. The pid is stamped where the PTY is created, but the poll is what
// keeps it true: it is the only thing that runs again, so it is what corrects a
// pane whose pid was lost and what clears one whose shell has gone.
//
// Nothing in the product zeroes the field by hand. It is zeroed here because a
// pane that arrived with no pid is the case the poll exists for: a session
// restored from disk, which carries no pid, and a daemon upgraded under a
// running client.
func TestThePollRestoresAShellPidThatWentMissing(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "repoll")

	state := sess.GetState()
	winID := state.Windows[0].ID
	want := sess.GetPTY(state.Windows[0].PTYID).ShellPID()
	if want <= 0 {
		t.Fatal("the daemon's own PTY reports no shell pid")
	}

	if err := sess.mutateState(func(st *SessionState) error {
		for i := range st.Windows {
			st.Windows[i].ShellPID = 0
		}
		return nil
	}); err != nil {
		t.Fatalf("mutateState: %v", err)
	}
	if pid := stateWindow(t, sess.GetState(), winID).ShellPID; pid != 0 {
		t.Fatalf("the pid did not clear: %d", pid)
	}

	// The count the poll returns is agent states only, so the pid coming back is
	// read off the state rather than out of the return.
	sess.applyAgentDetection(d.foregroundResolver(sess), d.agentMatcher.identifyDetail)
	if pid := stateWindow(t, sess.GetState(), winID).ShellPID; pid != want {
		t.Fatalf("the poll left shell pid %d, want %d", pid, want)
	}
}

// TestARestoredPaneCarriesItsNewShellPid closes the last gap in the field's
// life. Resurrection state carries no pid on purpose, so a restored session
// would otherwise sit uncheckable until the detector's next poll. The respawn
// already has the number.
func TestARestoredPaneCarriesItsNewShellPid(t *testing.T) {
	t.Cleanup(useResurrectionDir(t.TempDir()))

	cwd := t.TempDir()
	if err := SaveSessionForResurrection(&SessionState{
		Name: "restored", CurrentWorkspace: 1, Width: 120, Height: 40,
		Windows: []WindowState{
			{ID: "win-1", Title: "shell", Width: 60, Height: 40, Workspace: 1, PTYID: "dead-pty-1", Cwd: cwd},
		},
	}); err != nil {
		t.Fatalf("SaveSessionForResurrection: %v", err)
	}

	d := NewDaemon(&DaemonConfig{})
	d.restoreAllSessions()
	t.Cleanup(d.manager.Shutdown)

	sess := d.manager.GetSession("restored")
	if sess == nil {
		t.Fatal("the session was not restored")
	}
	state := sess.GetState()
	if len(state.Windows) != 1 {
		t.Fatalf("the restored session holds %d windows, want 1", len(state.Windows))
	}
	w := state.Windows[0]
	pty := sess.GetPTY(w.PTYID)
	if pty == nil {
		t.Fatal("the restored window has no PTY")
	}
	if w.ShellPID != pty.ShellPID() || w.ShellPID <= 0 {
		t.Fatalf("the restored pane carries shell pid %d, want the respawned shell's %d", w.ShellPID, pty.ShellPID())
	}
}

// TestANewPaneCarriesItsShellPidBeforeAnyPoll pins the stamp at PTY creation on
// its own. The detector's poll would fill the pid in within two seconds anyway,
// so nothing else in this file can tell the two sources apart; this one can,
// because the session it builds has no daemon behind it and therefore no poll.
//
// Those two seconds are the point. A pane the rail cannot check is a pane whose
// file actions are live on whatever directory it names, and a pane is at its
// most talkative in the moment it starts.
func TestANewPaneCarriesItsShellPidBeforeAnyPoll(t *testing.T) {
	t.Cleanup(useResurrectionDir(t.TempDir()))

	sess, err := NewSession("fresh", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(sess.Stop)
	if _, err := sess.AddDaemonWindow("Window", nil); err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}

	w := sess.GetState().Windows[0]
	pty := sess.GetPTY(w.PTYID)
	if pty == nil {
		t.Fatal("the new window has no PTY")
	}
	if w.ShellPID != pty.ShellPID() || w.ShellPID <= 0 {
		t.Fatalf("a pane carries shell pid %d before its first poll, want its shell's %d", w.ShellPID, pty.ShellPID())
	}
}
