package session

import (
	"strings"
	"testing"
	"time"
)

// new-window with close_on_exit closes the window when its process exits,
// with no client attached. Without it, a detached session keeps the window.
func TestNewWindowCloseOnExitWithoutAClient(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "w")
	c := dialVerb(t, sp)
	open := func(close bool) string {
		t.Helper()
		got := result(t, callP(c, t, "new-window", map[string]any{
			"session": "w", "command": []string{"sh", "-c", "exit 0"}, "close_on_exit": close, "focus": false,
		}))
		id, _ := got["window_id"].(string)
		if id == "" {
			t.Fatalf("new-window = %v", got)
		}
		return id
	}
	has := func(id string) bool {
		for _, w := range sess.GetState().Windows {
			if w.ID == id {
				return true
			}
		}
		return false
	}
	closing := open(true)
	kept := open(false)
	deadline := time.Now().Add(5 * time.Second)
	for has(closing) {
		if time.Now().After(deadline) {
			t.Fatal("the close_on_exit window stayed after its process exited")
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	if !has(kept) {
		t.Fatal("a window without close_on_exit closed on a detached session")
	}
}

// new-window with keep_open and wait answers with the program's exit status,
// keeps the window, and shows the status under the last output. wait-for
// window-exit reports the same status. A signal is reported as the signal, so
// a killed program never reads as a success.
func TestNewWindowKeepOpenWaitReportsTheExitStatus(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "w")
	c := dialVerb(t, sp)

	got := result(t, callP(c, t, "new-window", map[string]any{
		"session": "w", "name": "tests", "focus": false, "keep_open": true, "wait": true,
		"command": []string{"sh", "-c", "echo last-line; exit 3"},
	}))
	if code, _ := got["exit_code"].(float64); code != 3 || got["keep_open"] != true {
		t.Fatalf("new-window wait = %v, want exit_code 3 and keep_open", got)
	}
	id, _ := got["window_id"].(string)
	if _, ok := findWindowState(sess.GetState(), id); !ok {
		t.Fatal("the keep_open window closed when its program exited")
	}
	text := capturePane(t, c, "w", id)
	if !strings.Contains(text, "last-line") || !strings.Contains(text, "The program exited with status 3.") {
		t.Fatalf("the pane shows %q, want the output and the exit line", text)
	}
	if !strings.Contains(text[strings.Index(text, "last-line"):], "status 3") {
		t.Fatalf("the exit line is above the program's output: %q", text)
	}

	exit := result(t, callP(c, t, "wait-for", map[string]any{"session": "w", "window": id, "condition": "window-exit"}))
	if code, _ := exit["exit_code"].(float64); code != 3 {
		t.Fatalf("wait-for window-exit = %v, want exit_code 3", exit)
	}

	killed := result(t, callP(c, t, "new-window", map[string]any{
		"session": "w", "focus": false, "wait": true,
		"command": []string{"sh", "-c", "kill -9 $$"},
	}))
	if sig, _ := killed["signal"].(float64); sig != 9 {
		t.Fatalf("new-window wait on a killed program = %v, want signal 9", killed)
	}
	if code, _ := killed["exit_code"].(float64); code == 0 {
		t.Fatalf("a killed program reported exit_code 0: %v", killed)
	}
}

// TestClosingAWindowLeavesNothingOfItsAgentStores: every per-pane store the
// session keeps for an agent is dropped when the window closes, whether or not
// the agent detector runs to sweep them. Only the claim, the harness process
// and the shared guard were dropped there; the turn, the subagents, the seen
// completion, the folders its agents started in and a shown question stayed for
// the life of the session.
func TestClosingAWindowLeavesNothingOfItsAgentStores(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess, a, _ := twoWindowSession(t, d, "work")

	sess.noteAgentStartDir(a, "agent-sid", "/work/project")
	sess.showAsk(a, "req-1", "Ship it?")
	sess.stateMu.Lock()
	sess.agentTurns = map[string]agentTurn{a: {since: 1}}
	sess.agentSubagents = map[string]*paneSubagents{a: {}}
	sess.completionSeen = map[string]uint64{a: 3}
	sess.agentClaims = map[string]agentClaim{a: {}}
	sess.stateMu.Unlock()
	sess.startDirs.Store(a, "/work")

	if _, err := sess.CloseDaemonWindow(a, "a test"); err != nil {
		t.Fatalf("CloseDaemonWindow: %v", err)
	}

	sess.stateMu.RLock()
	defer sess.stateMu.RUnlock()
	left := map[string]int{
		"agentClaims":    len(sess.agentClaims),
		"agentTurns":     len(sess.agentTurns),
		"agentSubagents": len(sess.agentSubagents),
		"completionSeen": len(sess.completionSeen),
		"askShows":       len(sess.askShows),
	}
	sess.agentDirsMu.Lock()
	left["agentDirs"] = len(sess.agentDirs)
	sess.agentDirsMu.Unlock()
	if _, ok := sess.startDirs.Load(a); ok {
		left["startDirs"] = 1
	}
	for name, n := range left {
		if n != 0 {
			t.Errorf("%s still holds %d entries for a window that closed", name, n)
		}
	}
}
